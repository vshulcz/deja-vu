package index

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync/atomic"
)

// recordOffsetsFile lists, per session, where its records sit in records.bin.
// Loading one session used to walk the whole log, 200 MB on a real store, for
// every caller that wanted a few sessions: the session-start hook, recall's
// lineage and identity lookups, the friction texts.
//
// It is a cache the log is checked against, never the other way round. The
// header names how much of the log it covers and fingerprints that span, so a
// log rewritten under it is read whole as before; records past the covered
// span (an append since the table was written) are read by walking that tail
// alone. A table missing, stale or unreadable costs the walk it replaced and
// nothing else.
const recordOffsetsFile = "recoffsets.bin"

var recordOffsetsMagic = [8]byte{'D', 'J', 'R', 'O', 'F', 'F', '0', '1'}

// recordOffsetsHeaderSize is the magic, the covered size, the record count,
// the fingerprint and the key count.
const recordOffsetsHeaderSize = 8 + 8 + 8 + sha256.Size + 4

// recordOffsetsRefreshFloor is the tail a pass leaves to the readers' walk
// before it writes the table again. Readers walk an uncovered tail anyway, and
// rewriting the table on every append would cost each hook pass a few MB of
// writes for a tail a reader skips in a millisecond.
var recordOffsetsRefreshFloor int64 = 4 << 20

// recordOffsetsRefreshShare is the same floor as a share of the covered log.
var recordOffsetsRefreshShare int64 = 16

// recordLogFingerprint hashes three windows of the log's first size bytes:
// its start, its middle and its end. A log rewritten under a table changes
// at least one of them unless the rewrite reproduced those bytes exactly.
func recordLogFingerprint(f *os.File, size int64) ([sha256.Size]byte, error) {
	const window = 4096
	h := sha256.New()
	var lenb [8]byte
	binary.LittleEndian.PutUint64(lenb[:], uint64(size)) //nolint:gosec // file sizes are non-negative
	h.Write(lenb[:])
	buf := make([]byte, window)
	for _, at := range []int64{0, size / 2, size - window} {
		if at < 0 {
			at = 0
		}
		n := int64(window)
		if at+n > size {
			n = size - at
		}
		if n <= 0 {
			continue
		}
		if _, err := f.ReadAt(buf[:n], at); err != nil {
			return [sha256.Size]byte{}, err
		}
		h.Write(buf[:n])
	}
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

func keyHash(key string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return h.Sum64()
}

// recordOffsets is a decoded table: every key's record offsets, ascending.
type recordOffsets struct {
	covered int64
	records int64
	byKey   map[string][]int64
}

// scanRecordOffsets walks records.bin from start to end, reading only each
// record's key, and adds every record's offset under its key.
func scanRecordOffsets(f *os.File, t *recordTables, start, end int64, into map[string][]int64) (int64, error) {
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return 0, err
	}
	r := bufio.NewReaderSize(io.LimitReader(f, end-start), 1024*1024)
	off := start
	dead := t.deadFrom(start)
	var n, scanned int64
	var hdr [4]byte
	for off < end {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return 0, err
		}
		size := int64(binary.LittleEndian.Uint32(hdr[:]))
		if size > maxRecordSize {
			return 0, fmt.Errorf("%w: record length %d exceeds cap", errCorruptIndex, size)
		}
		head, err := r.Peek(int(min(size, binary.MaxVarintLen64)))
		if err != nil && len(head) == 0 {
			return 0, err
		}
		kid, un := binary.Uvarint(head)
		if un <= 0 {
			return 0, errShortRecord
		}
		if !dead.at(off) {
			key := t.lookup(kid)
			into[key] = append(into[key], off)
			n++
		}
		if _, err := r.Discard(int(size)); err != nil {
			return 0, err
		}
		off += 4 + size
		scanned++
	}
	atomic.AddInt64(&recordOffsetsScanned, scanned)
	return n, nil
}

// recordOffsetsScanned counts the records walked to build offset tables; a
// test tells a pass that wrote its own table from one that walked the log.
var recordOffsetsScanned int64

// writeRecordOffsets brings the table in dir up to the first size bytes of
// its log. A table that still covers a prefix of the log is extended by
// walking the rest; anything else is built from a walk of the whole log. It
// is skipped while the uncovered tail is small, which readers walk themselves.
func writeRecordOffsets(dir string, t *recordTables, size int64) error {
	path := filepath.Join(dir, "records.bin")
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if fi, statErr := f.Stat(); statErr != nil {
		return statErr
	} else if fi.Size() < size {
		size = fi.Size()
	}
	byKey := map[string][]int64{}
	start, records := int64(0), int64(0)
	if covered, ok := recordOffsetsCover(dir, f); ok && covered <= size {
		if size-covered < max(recordOffsetsRefreshFloor, covered/recordOffsetsRefreshShare) || size == covered {
			return nil
		}
		if old, offsetsErr := readRecordOffsetsFor(dir, f, nil); offsetsErr == nil {
			byKey, start, records = old.byKey, old.covered, old.records
		}
	}
	n, err := scanRecordOffsets(f, t, start, size, byKey)
	if err != nil {
		// A log the walk cannot read to the end gets no table: readers walk it
		// as they did before, and say what is wrong with it.
		_ = os.Remove(filepath.Join(dir, recordOffsetsFile))
		return err
	}
	fp, err := recordLogFingerprint(f, size)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, recordOffsetsFile), encodeRecordOffsets(size, records+n, fp, byKey))
}

func encodeRecordOffsets(covered, records int64, fp [sha256.Size]byte, byKey map[string][]int64) []byte {
	type entry struct {
		hash uint64
		key  string
	}
	entries := make([]entry, 0, len(byKey))
	for k := range byKey {
		entries = append(entries, entry{keyHash(k), k})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].hash != entries[j].hash {
			return entries[i].hash < entries[j].hash
		}
		return entries[i].key < entries[j].key
	})
	var data bytes.Buffer
	dir := make([]byte, 0, 16*len(entries))
	var tmp [binary.MaxVarintLen64]byte
	for _, e := range entries {
		dir = binary.LittleEndian.AppendUint64(dir, e.hash)
		dir = binary.LittleEndian.AppendUint64(dir, uint64(data.Len())) //nolint:gosec // lengths are non-negative
		data.Write(tmp[:binary.PutUvarint(tmp[:], uint64(len(e.key)))])
		data.WriteString(e.key)
		offs := byKey[e.key]
		// Deltas, so in log order, as a walk lists them. A pass appending to
		// the log gathers out of order a turn a rewrite moves to a session's
		// kept copy: written at the end, it comes ahead of the copy's own
		// records, which stay where they were (#5522).
		if !slices.IsSorted(offs) {
			slices.Sort(offs)
		}
		data.Write(tmp[:binary.PutUvarint(tmp[:], uint64(len(offs)))])
		prev := int64(0)
		for _, o := range offs {
			data.Write(tmp[:binary.PutUvarint(tmp[:], uint64(o-prev))]) //nolint:gosec // offsets ascend
			prev = o
		}
	}
	out := make([]byte, 0, recordOffsetsHeaderSize+len(dir)+data.Len())
	out = append(out, recordOffsetsMagic[:]...)
	out = binary.LittleEndian.AppendUint64(out, uint64(covered)) //nolint:gosec // file sizes are non-negative
	out = binary.LittleEndian.AppendUint64(out, uint64(records)) //nolint:gosec // record counts are non-negative
	out = append(out, fp[:]...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(entries))) //nolint:gosec // keys are far fewer than 2^32
	out = append(out, dir...)
	return append(out, data.Bytes()...)
}

var errNoRecordOffsets = errors.New("no record offsets")

// recordOffsetsCover reads only the table's header and reports how much of
// the open log f it covers, when it still fits f.
func recordOffsetsCover(dir string, f *os.File) (int64, bool) {
	tf, err := os.Open(filepath.Join(dir, recordOffsetsFile))
	if err != nil {
		return 0, false
	}
	defer func() { _ = tf.Close() }()
	var b [recordOffsetsHeaderSize]byte
	if _, err = io.ReadFull(tf, b[:]); err != nil || !bytes.Equal(b[:8], recordOffsetsMagic[:]) {
		return 0, false
	}
	covered := int64(binary.LittleEndian.Uint64(b[8:])) //nolint:gosec // written from a non-negative size
	if fi, statErr := f.Stat(); statErr != nil || fi.Size() < covered {
		return 0, false
	}
	got, err := recordLogFingerprint(f, covered)
	if err != nil || !bytes.Equal(got[:], b[24:24+sha256.Size]) {
		return 0, false
	}
	return covered, true
}

// readRecordOffsetsFor reads the table in dir and checks it against the open
// log f. With keys it decodes only those keys' entries; with nil keys, all of
// them.
func readRecordOffsetsFor(dir string, f *os.File, keys map[string]bool) (recordOffsets, error) {
	b, err := os.ReadFile(filepath.Join(dir, recordOffsetsFile))
	if err != nil {
		return recordOffsets{}, errNoRecordOffsets
	}
	if len(b) < recordOffsetsHeaderSize || !bytes.Equal(b[:8], recordOffsetsMagic[:]) {
		return recordOffsets{}, errNoRecordOffsets
	}
	covered := int64(binary.LittleEndian.Uint64(b[8:]))  //nolint:gosec // written from a non-negative size
	records := int64(binary.LittleEndian.Uint64(b[16:])) //nolint:gosec // written from a non-negative count
	var fp [sha256.Size]byte
	copy(fp[:], b[24:24+sha256.Size])
	nkeys := int(binary.LittleEndian.Uint32(b[24+sha256.Size:]))
	dirStart := recordOffsetsHeaderSize
	dataStart := dirStart + 16*nkeys
	if nkeys < 0 || dataStart > len(b) {
		return recordOffsets{}, errNoRecordOffsets
	}
	if fi, err := f.Stat(); err != nil || fi.Size() < covered {
		return recordOffsets{}, errNoRecordOffsets
	}
	if got, err := recordLogFingerprint(f, covered); err != nil || got != fp {
		return recordOffsets{}, errNoRecordOffsets
	}
	out := recordOffsets{covered: covered, records: records, byKey: map[string][]int64{}}
	entryAt := func(i int) (uint64, int) {
		p := dirStart + 16*i
		return binary.LittleEndian.Uint64(b[p:]), int(binary.LittleEndian.Uint64(b[p+8:])) //nolint:gosec // written from a non-negative offset
	}
	decode := func(at int, want string) bool {
		p := dataStart + at
		if p < dataStart || p >= len(b) {
			return false
		}
		kl, n := binary.Uvarint(b[p:])
		if n <= 0 || kl > uint64(len(b)-p-n) { //nolint:gosec // n bytes were read from b[p:]
			return false
		}
		p += n
		key := string(b[p : p+int(kl)]) //nolint:gosec // kl is checked against len(b) above
		p += int(kl)                    //nolint:gosec // kl is checked against len(b) above
		if want != "" && key != want {
			return true
		}
		cnt, n := binary.Uvarint(b[p:])
		if n <= 0 {
			return false
		}
		p += n
		offs := make([]int64, 0, cnt)
		prev := int64(0)
		for range cnt {
			d, n := binary.Uvarint(b[p:])
			if n <= 0 {
				return false
			}
			p += n
			prev += int64(d) //nolint:gosec // deltas of ascending offsets
			offs = append(offs, prev)
		}
		out.byKey[key] = offs
		return true
	}
	if keys == nil {
		for i := range nkeys {
			_, at := entryAt(i)
			if !decode(at, "") {
				return recordOffsets{}, errNoRecordOffsets
			}
		}
		return out, nil
	}
	for key := range keys {
		h := keyHash(key)
		i := sort.Search(nkeys, func(i int) bool { e, _ := entryAt(i); return e >= h })
		for ; i < nkeys; i++ {
			e, at := entryAt(i)
			if e != h {
				break
			}
			if !decode(at, key) {
				return recordOffsets{}, errNoRecordOffsets
			}
		}
	}
	return out, nil
}

// offsetsWalkShare: past one record in this many, the wanted records are read
// by walking the log rather than one by one.
var offsetsWalkShare int64 = 4

// recordOffsetReads counts keyed reads the table answered; tests use it to
// tell the table path from the walk.
var recordOffsetReads int64

// errOffsetsMismatch is a table entry that does not point at a record of its
// key: the table is wrong, and the caller walks the log instead.
var errOffsetsMismatch = errors.New("record offsets do not match the log")

// eachRecordForKeysByOffsets is eachRecordForKeysInRoles answered from the
// table: the wanted keys' records inside the covered span by offset, then a
// walk of the tail past it. ok is false when the table cannot answer, before
// fn has been called.
func eachRecordForKeysByOffsets(f *os.File, dir string, t *recordTables, want, roles map[string]bool, fn func(Record)) (bool, error) {
	ro, err := readRecordOffsetsFor(dir, f, want)
	if err != nil {
		return false, nil //nolint:nilerr // no table: the caller walks the log
	}
	var offs []int64
	for k := range want {
		offs = append(offs, ro.byKey[k]...)
	}
	// Many sessions at once are walked: their records are held until the
	// table is known to be right, and a walk streams them instead. One session
	// is read by offset however long it is, since its caller holds all of it
	// anyway, and a marathon of a third of the log walked all 200 MB of it.
	if len(want) > 1 && ro.records > 0 && int64(len(offs)) > ro.records/offsetsWalkShare {
		return false, nil
	}
	atomic.AddInt64(&recordOffsetReads, 1)
	slices.Sort(offs)
	offs = slices.DeleteFunc(offs, t.isDead)
	recs := make([]Record, 0, len(offs))
	// In spans, the way eachRecordAt reads postings: a session's records sit
	// back to back in the log, and a pread per record was most of what loading
	// a long session cost.
	var buf []byte
	for i := 0; i < len(offs); {
		j := i + 1
		for j < len(offs) && offs[j]-offs[j-1] <= spanGapMax && offs[j]-offs[i] < spanSizeMax {
			j++
		}
		start := offs[i]
		size := int(offs[j-1]-start) + readAheadSize
		if cap(buf) < size {
			buf = make([]byte, size)
		}
		b := buf[:size]
		n, err := f.ReadAt(b, start)
		if err != nil && !errors.Is(err, io.EOF) {
			n = 0
		}
		b = b[:n]
		for _, off := range offs[i:j] {
			rec, keep, whole, err := recordInSpan(b, int(off-start), t, want, roles)
			if err == nil && !whole {
				rec, keep, err = readRecordAtInRoles(f, off, t, want, roles)
			}
			if err != nil {
				if errors.Is(err, errOffsetsMismatch) {
					return false, nil
				}
				return false, err
			}
			if keep {
				recs = append(recs, rec)
			}
		}
		i = j
	}
	for _, rec := range recs {
		fn(rec)
	}
	if _, err := f.Seek(ro.covered, io.SeekStart); err != nil {
		return true, err
	}
	return true, walkRecordsForKeys(bufio.NewReaderSize(f, 256*1024), ro.covered, t, want, roles, fn)
}

// recordInSpan is readRecordAtInRoles over a span already read: whole is false
// when the span ends before the record does, and the caller reads it alone.
func recordInSpan(b []byte, rel int, t *recordTables, want, roles map[string]bool) (rec Record, keep, whole bool, err error) {
	if rel < 0 || rel+4 > len(b) {
		return Record{}, false, false, nil
	}
	size := int(binary.LittleEndian.Uint32(b[rel : rel+4]))
	if size > maxRecordSize {
		return Record{}, false, true, errOffsetsMismatch
	}
	if rel+4+size > len(b) {
		return Record{}, false, false, nil
	}
	payload := b[rel+4 : rel+4+size]
	key, role, ok := recordRoleIn(payload, t)
	if !ok || !want[key] {
		return Record{}, false, true, errOffsetsMismatch
	}
	if roles != nil && !roles[role] {
		return Record{}, false, true, nil
	}
	rec, derr := decodeRecord(payload, t)
	if derr != nil {
		return Record{}, false, true, fmt.Errorf("%w: %w", errCorruptIndex, derr)
	}
	return rec, true, true, nil
}

// readRecordAtInRoles reads the record at off, checking it belongs to a wanted
// key, and decodes it when its role is wanted.
func readRecordAtInRoles(f *os.File, off int64, t *recordTables, want, roles map[string]bool) (Record, bool, error) {
	buf := readAheadPool.Get().(*[]byte) //nolint:forcetypeassert // the pool holds only *[]byte
	defer readAheadPool.Put(buf)
	b := *buf
	n, err := f.ReadAt(b, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return Record{}, false, err
	}
	if n < 4 {
		return Record{}, false, errOffsetsMismatch
	}
	size := int(binary.LittleEndian.Uint32(b[:4]))
	if size > maxRecordSize {
		return Record{}, false, errOffsetsMismatch
	}
	head := b[4:n]
	key, role, ok := recordRoleIn(head, t)
	if !ok || !want[key] {
		return Record{}, false, errOffsetsMismatch
	}
	if roles != nil && !roles[role] {
		return Record{}, false, nil
	}
	var payload []byte
	if size <= len(head) {
		payload = head[:size]
	} else {
		payload = make([]byte, size)
		m, readErr := f.ReadAt(payload, off+4)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return Record{}, false, readErr
		}
		if m < size {
			return Record{}, false, errOffsetsMismatch
		}
	}
	rec, err := decodeRecord(payload, t)
	if err != nil {
		return Record{}, false, fmt.Errorf("%w: %w", errCorruptIndex, err)
	}
	return rec, true, nil
}

// writeFileAtomic writes b beside p and renames it over p. No fsync: the
// table is a cache checked against the log, and a torn one is refused.
func writeFileAtomic(p string, b []byte) error {
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := renameWaiting(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// passOffsets is the offset table of a log a pass writes from scratch,
// gathered as each record is written.
type passOffsets struct {
	byKey   map[string][]int64
	records int64
}

func (p *passOffsets) add(key string, off int64) {
	p.byKey[key] = append(p.byKey[key], off)
	p.records++
}

// write commits the table for the log in dir, which holds exactly the
// records gathered.
func (p *passOffsets) write(dir string) error {
	size, fp, err := logFingerprint(filepath.Join(dir, "records.bin"))
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, recordOffsetsFile), encodeRecordOffsets(size, p.records, fp, p.byKey))
}
