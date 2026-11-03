package index

import (
	"bufio"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vshulcz/deja-vu/internal/policy"
)

// recordLogScans counts keyed passes over the record log. The offset table
// (record_offsets.go) turns most of them into a few reads, but a pass without
// one still walks the whole of records.bin, so a caller asking for sessions
// one at a time is still a cost bug (#1069). Exported through RecordLogScans
// for tests that assert a batch path stayed one pass.
var recordLogScans int64

// RecordLogScans reports how many times this process walked the record log.
func RecordLogScans() int64 { return atomic.LoadInt64(&recordLogScans) }

// ReadRecords reads every record in the log with its offset.
func ReadRecords(dir string) ([]OffsetRecord, error) {
	t, err := loadRecordTables(dir)
	if err != nil {
		return nil, err
	}
	var out []OffsetRecord
	f, err := openIndexFile(filepath.Join(dir, "records.bin"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	w := newLogWalk(f, t, 0)
	for {
		off, payload, err := w.next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		r, err := decodeRecord(payload, t)
		if err != nil {
			return nil, err
		}
		// A key the table does not name is a record a pass is still writing:
		// not in this snapshot, and not to be embedded or sent anywhere.
		if r.Key == "" {
			continue
		}
		out = append(out, OffsetRecord{Offset: off, Record: r})
	}
}

// logWalk reads the record log in order, handing back each live record's
// offset and payload and passing over the dead ones unread. The payload is
// reused by the next call.
type logWalk struct {
	r    *bufio.Reader
	off  int64
	dead deadCursor
	buf  []byte
}

func newLogWalk(r io.Reader, t *recordTables, from int64) *logWalk {
	return &logWalk{r: bufio.NewReaderSize(r, 1024*1024), off: from, dead: t.deadFrom(from)}
}

// next is the next live record. A clean or torn end of the log is io.EOF or
// io.ErrUnexpectedEOF.
func (w *logWalk) next() (int64, []byte, error) {
	var hdr [4]byte
	for {
		if _, err := io.ReadFull(w.r, hdr[:]); err != nil {
			return 0, nil, err
		}
		n := binary.LittleEndian.Uint32(hdr[:])
		if n > maxRecordSize {
			return 0, nil, fmt.Errorf("%w: record length %d exceeds cap", errCorruptIndex, n)
		}
		at := w.off
		w.off += 4 + int64(n)
		if w.dead.at(at) {
			if _, err := w.r.Discard(int(n)); err != nil {
				return 0, nil, err
			}
			continue
		}
		if cap(w.buf) < int(n) {
			w.buf = make([]byte, n)
		}
		payload := w.buf[:n]
		if _, err := io.ReadFull(w.r, payload); err != nil {
			return 0, nil, err
		}
		return at, payload, nil
	}
}

// Generation names the index's current build, or its build time for an
// index written before builds were named.
func Generation(dir string) (string, error) {
	m, err := readManifest(dir)
	if err != nil {
		return "", err
	}
	gen := m.Generation
	if gen == "" {
		gen = m.BuiltAt.UTC().Format(time.RFC3339Nano)
	}
	// The stamp alone is a moment, and two rebuilds inside one tick of a coarse
	// clock share it — a forget straight after an embed did, on Linux CI. What a
	// caller actually asks this is whether record offsets still mean what they
	// meant, so records.bin's length goes in the answer: same length and same
	// stamp is the one case where a sidecar built earlier is still correct
	// (#1355).
	return gen + "+" + strconv.FormatInt(m.RecordsSize, 10), nil
}

func newRecordWriter(f *os.File, t *recordTables) (*recordWriter, error) {
	off, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	return &recordWriter{f: f, w: bufio.NewWriterSize(f, 1<<20), off: off, tables: t}, nil
}

func (rw *recordWriter) write(r Record) (int64, error) {
	b := encodeRecord(r, rw.tables)
	if len(b) > 1<<31 {
		return 0, errors.New("record too large")
	}
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(b))) //nolint:gosec // a record is far below 4 GiB
	if _, err := rw.w.Write(hdr[:]); err != nil {
		return 0, err
	}
	if _, err := rw.w.Write(b); err != nil {
		return 0, err
	}
	off := rw.off
	rw.off += int64(len(hdr)) + int64(len(b))
	return off, nil
}

func (rw *recordWriter) Close() error {
	ferr := rw.w.Flush()
	// The manifest stamps the record-log size on commit; sync data first so a
	// crash cannot leave a manifest that promises records the disk never got.
	serr := rw.f.Sync()
	cerr := rw.f.Close()
	if ferr != nil {
		return ferr
	}
	if serr != nil {
		return serr
	}
	return cerr
}

func writeRecord(f *os.File, r Record, t *recordTables) (int64, error) {
	off, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	b := encodeRecord(r, t)
	if len(b) > 1<<31 {
		return 0, errors.New("record too large")
	}
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(b))) //nolint:gosec // a record is far below 4 GiB
	if _, err = f.Write(hdr[:]); err != nil {
		return 0, err
	}
	_, err = f.Write(b)
	return off, err
}

// readRecordAt reads one record without moving the file position.
//
// It used to Seek and then read the header and the payload, three syscalls per
// record. A search over a common term resolves thousands of records, and
// profiling put 70% of such a search inside this function — almost all of it in
// the kernel rather than in any decoding.
//
// The speculative read covers header and payload in one pread for anything
// under readAheadSize, which is nearly everything: the median indexed message
// is a few hundred bytes. A larger record costs a second read, which is the
// same work the old path did anyway.
func readRecordAt(f *os.File, off int64, t *recordTables) (Record, error) {
	buf := readAheadPool.Get().(*[]byte) //nolint:forcetypeassert // the pool holds only *[]byte
	defer readAheadPool.Put(buf)
	b := *buf

	n, err := f.ReadAt(b, off)
	// A record at the end of the file returns a short read with io.EOF, which
	// is not an error here — the record may still be entirely inside it.
	if err != nil && !errors.Is(err, io.EOF) {
		return Record{}, err
	}
	// Match what ReadFull did before: nothing at all is io.EOF, a partial
	// header is an unexpected one. A caller past the end of the file relies on
	// telling those apart.
	if n == 0 {
		return Record{}, io.EOF
	}
	if n < 4 {
		return Record{}, io.ErrUnexpectedEOF
	}
	size := binary.LittleEndian.Uint32(b[:4])
	if size > maxRecordSize {
		return Record{}, fmt.Errorf("%w: record length %d exceeds cap", errCorruptIndex, size)
	}
	if int(size)+4 <= n {
		return decodeRecord(b[4:4+size], t)
	}
	// Too big for the read-ahead: take the payload on its own.
	payload := make([]byte, size)
	m, err := f.ReadAt(payload, off+4)
	if err != nil && !errors.Is(err, io.EOF) {
		return Record{}, err
	}
	if m < int(size) {
		return Record{}, io.ErrUnexpectedEOF
	}
	return decodeRecord(payload, t)
}

// readAheadSize covers header plus payload for the overwhelming majority of
// records in one read; the p99 indexed message is about 8 KB.
const readAheadSize = 8 * 1024

var readAheadPool = sync.Pool{New: func() any { b := make([]byte, readAheadSize); return &b }}

// eachRecordUntil is eachRecord with a stop: fn returns false when it has seen
// enough, which is how an existence question avoids reading a log it has already
// answered.
func eachRecordUntil(path string, t *recordTables, fn func(Record) bool) error {
	f, err := openIndexFile(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w := newLogWalk(f, t, 0)
	for {
		_, payload, err := w.next()
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil
		}
		if err != nil {
			return err
		}
		rec, err := decodeRecord(payload, t)
		if err != nil {
			return err
		}
		if !fn(rec) {
			return nil
		}
	}
}

// recordRoleIn reads only what a record's prefix says: whose session it is and
// what kind of record it is. Nothing is inflated, which is the point.
func recordRoleIn(b []byte, t *recordTables) (key, role string, ok bool) {
	kid, n := binary.Uvarint(b)
	if n <= 0 {
		return "", "", false
	}
	b = b[n:]
	_, n = binary.Uvarint(b)
	if n <= 0 {
		return "", "", false
	}
	b = b[n:]
	rid, n := binary.Uvarint(b)
	if n <= 0 {
		return "", "", false
	}
	return t.lookup(kid), t.lookup(rid), true
}

func roleWanted(roles map[string]bool, role string) bool {
	if roles == nil || role == "" {
		return true
	}
	if v, ok := roles[role]; ok {
		return v
	}
	return roles["*"]
}

// eachRecordInRoles streams the records whose role is wanted, reading the
// prefix of every record and the body of only those. Same order and same
// records as filtering after a full decode, at a fraction of the work.
func eachRecordInRoles(path string, t *recordTables, want map[string]bool, fn func(Record)) error {
	f, err := openIndexFile(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w := newLogWalk(f, t, 0)
	for {
		_, payload, err := w.next()
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil
		}
		if err != nil {
			return err
		}
		_, role, ok := recordRoleIn(payload, t)
		if !ok {
			return errShortRecord
		}
		if !want[role] {
			continue
		}
		rec, err := decodeRecord(payload, t)
		if err != nil {
			return err
		}
		fn(rec)
	}
}

func eachRecord(path string, t *recordTables, fn func(Record)) error {
	return eachRecordUntil(path, t, func(r Record) bool {
		fn(r)
		return true
	})
}

// eachRecordForKeys walks the log decoding only records whose Key is in
// want; other bodies are skipped after peeking the key field. On a large log
// this trades a full decode of every record for a few length reads.
func eachRecordForKeys(path string, t *recordTables, want map[string]bool, fn func(Record)) error {
	return eachRecordForKeysInRoles(path, t, want, nil, fn)
}

// eachRecordForKeysInRoles is eachRecordForKeys that also skips, by the
// record's prefix, the bodies of wanted sessions whose role is not in roles.
// A nil roles keeps every role.
func eachRecordForKeysInRoles(path string, t *recordTables, want, roles map[string]bool, fn func(Record)) error {
	return eachRecordForKeysInRolesWindowed(path, t, want, roles, nil, fn)
}

func eachRecordForKeysInRolesWindowed(path string, t *recordTables, want, roles map[string]bool, matchedByKey map[string][]int64, fn func(Record)) error {
	atomic.AddInt64(&recordLogScans, 1)
	f, err := openIndexFile(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if ok, err := eachRecordForKeysByOffsetsWindowed(f, filepath.Dir(path), t, want, roles, matchedByKey, fn); ok || err != nil {
		return err
	}
	return walkRecordsForKeys(bufio.NewReaderSize(f, 1024*1024), 0, t, want, roles, fn)
}

// walkRecordsForKeys is the walk eachRecordForKeysInRoles does without a
// table, from wherever r stands, which is offset from, to the end of the log.
func walkRecordsForKeys(r *bufio.Reader, from int64, t *recordTables, want, roles map[string]bool, fn func(Record)) error {
	var hdr [4]byte
	dead := t.deadFrom(from)
	off := from
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			}
			return err
		}
		n := binary.LittleEndian.Uint32(hdr[:])
		if n > maxRecordSize {
			return fmt.Errorf("%w: record length %d exceeds cap", errCorruptIndex, n)
		}
		// The key id is the first varint of the payload, so peek it and skip
		// the rest of the record without touching it. Materializing every
		// record just to read two bytes copied the whole log per query.
		peek := min(int(n), binary.MaxVarintLen64)
		head, perr := r.Peek(peek)
		if perr != nil && len(head) == 0 {
			if perr == io.EOF || errors.Is(perr, io.ErrUnexpectedEOF) {
				return nil
			}
			return perr
		}
		kid, un := binary.Uvarint(head)
		if un <= 0 {
			return errShortRecord
		}
		at := off
		off += 4 + int64(n)
		skip := !want[t.lookup(kid)] || dead.at(at)
		if !skip && roles != nil {
			// Key, source and role lead the payload; the body after them is
			// only read for a role the caller wants.
			if head, _ := r.Peek(min(int(n), 3*binary.MaxVarintLen64)); len(head) > 0 {
				if _, role, ok := recordRoleIn(head, t); ok && !roleWanted(roles, role) {
					skip = true
				}
			}
		}
		if skip {
			if _, derr := r.Discard(int(n)); derr != nil {
				if derr == io.EOF || errors.Is(derr, io.ErrUnexpectedEOF) {
					return nil
				}
				return derr
			}
			continue
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(r, b); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			}
			return err
		}
		rec, derr := decodeRecord(b, t)
		if derr != nil {
			// The record was fully framed (a valid length prefix, n bytes read),
			// so a payload that will not decode is real corruption in the middle
			// of the log, not the tolerated in-flight tail that the EOF branches
			// above return nil for. Surface it like the length-cap check does so
			// the read path rebuilds instead of silently dropping the message.
			return fmt.Errorf("%w: %w", errCorruptIndex, derr)
		}
		fn(rec)
	}
}

// zeroTimeUnixNano is what the zero time serializes to; records.bin has
// always stored it, so it stays the on-disk marker for "never stamped".
var zeroTimeUnixNano = time.Time{}.UnixNano()

// UnixNano is only defined for 1678-2262: outside that window the nanosecond
// count wraps, and what comes back is a plausible date rather than an obvious
// one. A transcript stamped 2999 read back out of records.bin as 1829, so
// `deja stats` reported a store that began 200 years before its oldest
// session. Clamping keeps a nonsense stamp at the edge of the range instead of
// moving it to the other end of history.
var (
	minRecordTime = time.Unix(0, math.MinInt64)
	maxRecordTime = time.Unix(0, math.MaxInt64)
)

func recordNanos(t time.Time) int64 {
	switch {
	case t.IsZero():
		return zeroTimeUnixNano
	case t.Before(minRecordTime):
		return math.MinInt64
	case t.After(maxRecordTime):
		return math.MaxInt64
	}
	return t.UnixNano()
}

// recordTables interns the two fields every record used to repeat verbatim.
// A corpus of 57k messages held only 90 distinct source paths and 1073
// distinct keys, so writing them per record cost 7.3 MB — 15% of the record
// log — to store 1163 strings.
//
// The table is its own thing, deliberately not the session Ord: reusing Ord
// would make a record's identity depend on the manifest's Ord bookkeeping
// being right, turning an Ord bug from "postings merged" into "this message
// belongs to a different session". The table is written by the same pass that
// writes the records and swapped with them.
type recordTables struct {
	strs []string
	id   map[string]uint64
	// dead is the manifest's DeadRecords: records a walk of the log skips.
	dead []int64
}

// isDead reports whether the record at off was replaced by a later pass.
func (t *recordTables) isDead(off int64) bool {
	if t == nil || len(t.dead) == 0 {
		return false
	}
	_, ok := slices.BinarySearch(t.dead, off)
	return ok
}

// deadFrom is a cursor over the dead records at or past off, for a walk that
// visits the log in order.
func (t *recordTables) deadFrom(off int64) deadCursor {
	if t == nil || len(t.dead) == 0 {
		return deadCursor{}
	}
	i, _ := slices.BinarySearch(t.dead, off)
	return deadCursor{d: t.dead[i:]}
}

type deadCursor struct{ d []int64 }

// at reports whether the record at off is dead. Offsets must be asked in
// ascending order.
func (c *deadCursor) at(off int64) bool {
	for len(c.d) > 0 && c.d[0] < off {
		c.d = c.d[1:]
	}
	return len(c.d) > 0 && c.d[0] == off
}

func newRecordTables() *recordTables {
	return &recordTables{id: map[string]uint64{}}
}

// tablesFromStrings is the read path: resolving an id is a slice index, so
// the string->id map is left nil and built lazily only if something interns.
// Building it eagerly here cost a map of every string in the corpus on every
// search.
func tablesFromStrings(strs []string) *recordTables {
	return &recordTables{strs: strs}
}

func tablesFromManifest(m Manifest) *recordTables {
	t := tablesFromStrings(m.RecordStrings)
	t.dead = m.DeadRecords
	return t
}

func loadRecordTables(dir string) (*recordTables, error) {
	// Cached: this sits on the search path, and re-decoding the whole
	// manifest per query is what the interning was meant to avoid paying for.
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, err
	}
	return tablesFromManifest(m), nil
}

// fresh gives s an id of its own from here on, past every id the table had.
// Records written under it resolve to nothing in a table from before, which
// is how a reader of the previous manifest skips a tail appended for it.
func (t *recordTables) fresh(s string) {
	if before := len(t.strs); t.intern(s) >= uint64(before) {
		return // new to the table: its id is past every old one already
	}
	id := uint64(len(t.strs))
	t.strs = append(t.strs, s)
	t.id[s] = id
}

// intern returns the id for s, appending it on first sight. Ids are stable:
// the table only ever grows, so an appended record log keeps resolving.
func (t *recordTables) intern(s string) uint64 {
	if t.id == nil {
		// A zero-value table is usable; callers that build a writer by hand
		// should not have to know about the map.
		t.id = make(map[string]uint64, len(t.strs)+8)
		for i, v := range t.strs {
			if _, ok := t.id[v]; !ok {
				t.id[v] = uint64(i)
			}
		}
	}
	if id, ok := t.id[s]; ok {
		return id
	}
	id := uint64(len(t.strs))
	t.strs = append(t.strs, s)
	t.id[s] = id
	return id
}

func (t *recordTables) lookup(id uint64) string {
	if id < uint64(len(t.strs)) {
		return t.strs[id]
	}
	return ""
}

// compressFloor used to be 8 KB, from a run that compressed every message at
// the default level: the record log halved, but index builds doubled and a
// query that materializes many records tripled. That left 99.9% of records
// stored as plain text and records.bin bigger than the transcripts. At the
// fastest level and a 256-byte floor the day-zero pile's index went from 277
// to 194 MB for 3-5% more build CPU and no measurable query cost. Below 256
// bytes deflate rarely wins anything. Readers go by the per-record flag, so
// an index mixing both encodings needs no version bump.
const compressFloor = 256

const (
	recordRaw      = 0
	recordDeflated = 1
)

var deflateWriters = sync.Pool{New: func() any { w, _ := flate.NewWriter(nil, flate.BestSpeed); return w }}
var inflateReaders = sync.Pool{New: func() any { return flate.NewReader(nil) }}

// A record is [keyID][pathID][flag][payload]. The ids stay outside the
// payload so a walk can peek the key and skip the record without inflating
// anything — that path never touches a compressed byte.
func encodeRecord(r Record, t *recordTables) []byte {
	body := make([]byte, 0, len(r.Text)+24)
	body = binary.LittleEndian.AppendUint64(body, uint64(recordNanos(r.Time))) //nolint:gosec // a signed time's bits, read back as int64
	body = appendField(body, r.Text)

	b := make([]byte, 0, len(body)+16)
	b = binary.AppendUvarint(b, t.intern(r.Key))
	b = binary.AppendUvarint(b, t.intern(r.SourcePath))
	// The role sits outside the compressed body so a scan for one kind of
	// record can skip the rest without inflating them. It used to be the first
	// field inside the body, which made "every command in the store" — what
	// `deja how` asks — decompress all 280,000 records to read 20,000 of them:
	// 53% of that command's time was flate.
	b = binary.AppendUvarint(b, t.intern(r.Role))
	if len(body) < compressFloor {
		return append(append(b, recordRaw), body...)
	}
	var buf bytes.Buffer
	w := deflateWriters.Get().(*flate.Writer) //nolint:forcetypeassert // the pool holds only *flate.Writer
	w.Reset(&buf)
	if _, err := w.Write(body); err != nil {
		deflateWriters.Put(w)
		return append(append(b, recordRaw), body...)
	}
	err := w.Close()
	deflateWriters.Put(w)
	// Incompressible payloads (already-compressed blobs, random ids) are
	// stored as they are rather than grown.
	if err != nil || buf.Len() >= len(body) {
		return append(append(b, recordRaw), body...)
	}
	return append(append(b, recordDeflated), buf.Bytes()...)
}

func appendField(b []byte, s string) []byte {
	b = binary.AppendUvarint(b, uint64(len(s)))
	return append(b, s...)
}

// errShortRecord is a framed record whose payload does not hold what every
// record holds. The frame was read whole, so this is not a torn tail: it is a
// hole in the log, such as a frame a crash left zeroed, and it is corruption.
// It used to be io.ErrUnexpectedEOF, which every walker reads as the end of
// the file, so a hole mid-log silently ended the walk there.
var errShortRecord = fmt.Errorf("%w: a record ends before its fields do", errCorruptIndex)

func decodeRecord(b []byte, t *recordTables) (Record, error) {
	var rec Record
	var ok bool
	kid, n := binary.Uvarint(b)
	if n <= 0 {
		return rec, errShortRecord
	}
	b = b[n:]
	pid, n := binary.Uvarint(b)
	if n <= 0 {
		return rec, errShortRecord
	}
	b = b[n:]
	rid, n := binary.Uvarint(b)
	if n <= 0 {
		return rec, errShortRecord
	}
	b = b[n:]
	rec.Key = t.lookup(kid)
	rec.SourcePath = t.lookup(pid)
	rec.Role = t.lookup(rid)
	if len(b) < 1 {
		return rec, errShortRecord
	}
	flag := b[0]
	b = b[1:]
	if flag == recordDeflated {
		zr := inflateReaders.Get().(io.ReadCloser)                                 //nolint:forcetypeassert // the pool holds only flate readers
		if err := zr.(flate.Resetter).Reset(bytes.NewReader(b), nil); err != nil { //nolint:forcetypeassert // a flate reader is a Resetter
			inflateReaders.Put(zr)
			return rec, err
		}
		body, err := io.ReadAll(zr)
		inflateReaders.Put(zr)
		if err != nil {
			return rec, err
		}
		b = body
	} else if flag != recordRaw {
		return rec, fmt.Errorf("%w: unknown record encoding %d", errCorruptIndex, flag)
	}
	if len(b) < 8 {
		return rec, errShortRecord
	}
	// time.Time{}.UnixNano() is a large negative number that time.Unix turns
	// into the year 1754, so an unstamped message never satisfied IsZero()
	// again. Sync then read it as older than any watermark and skipped it on
	// every push — the message could never reach another machine, silently.
	if n := int64(binary.LittleEndian.Uint64(b[:8])); n == zeroTimeUnixNano { //nolint:gosec // a signed time's bits, written as uint64
		rec.Time = time.Time{}
	} else {
		rec.Time = time.Unix(0, n)
	}
	b = b[8:]
	if rec.Text, ok = consumeField(b); !ok {
		return rec, errShortRecord
	}
	return rec, nil
}

func consumeField(b []byte) (string, bool) {
	n, used := binary.Uvarint(b)
	if used <= 0 || uint64(len(b)-used) < n { //nolint:gosec // used <= len(b)
		return "", false
	}
	start := used
	end := start + int(n) //nolint:gosec // n is checked against len(b) above
	return string(b[start:end]), true
}

// swapIndexDir replaces dir with tmp without a destructive window: the old
// dir is parked as dir.old until the new one is in place, so a crash between
func eachRecordAt(f *os.File, offsets []int64, t *recordTables, fn func(Record)) error {
	return eachRecordAtInRoles(f, offsets, t, nil, fn)
}

func eachRecordAtInRoles(f *os.File, offsets []int64, t *recordTables, roles map[string]bool, fn func(Record)) error {
	buf := make([]byte, 0, 64*1024)
	for i := 0; i < len(offsets); {
		j := i + 1
		for j < len(offsets) &&
			offsets[j]-offsets[j-1] <= spanGapMax &&
			offsets[j]-offsets[i] < spanSizeMax {
			j++
		}
		start := offsets[i]
		want := int(offsets[j-1]-start) + readAheadSize
		if cap(buf) < want {
			buf = make([]byte, want)
		}
		b := buf[:want]
		// A failed span is not fatal: every record in it falls back to its own
		// read below, which is exactly what the caller used to do for all of
		// them. Aborting here would silently drop the rest of the results.
		n, err := f.ReadAt(b, start)
		if err != nil && !errors.Is(err, io.EOF) {
			n = 0
		}
		b = b[:n]
		for _, off := range offsets[i:j] {
			if t.isDead(off) {
				continue
			}
			rel := int(off - start)
			if roles != nil && rel >= 0 && rel+4 < len(b) {
				size := binary.LittleEndian.Uint32(b[rel : rel+4])
				if size <= maxRecordSize {
					end := min(len(b), rel+4+int(size))
					if _, role, ok := recordRoleIn(b[rel+4:end], t); ok && !roleWanted(roles, role) {
						continue
					}
				}
			}
			if r, ok := decodeRecordIn(b, rel, t); ok {
				fn(r)
				continue
			}
			// decodeRecordIn only failed because the span did not hold the whole
			// record, so re-read it on its own. This offset came from a committed
			// posting, so it must resolve: a torn tail (EOF) is tolerated like the
			// scan paths. Anything else is surfaced rather than dropping the
			// message — readRecordAt already tags a corrupt record with
			// errCorruptIndex and leaves a real IO error (a closed file, a denied
			// read) as itself, so it is returned unwrapped and each keeps its kind.
			r, err := readRecordAt(f, off, t)
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
					continue
				}
				return err
			}
			if !roleWanted(roles, r.Role) {
				continue
			}
			fn(r)
		}
		i = j
	}
	return nil
}

// decodeRecordIn decodes the record at rel inside an already-read span, and
// reports whether the span actually held all of it.
func decodeRecordIn(b []byte, rel int, t *recordTables) (Record, bool) {
	if rel < 0 || rel+4 > len(b) {
		return Record{}, false
	}
	size := binary.LittleEndian.Uint32(b[rel : rel+4])
	if size > maxRecordSize || rel+4+int(size) > len(b) {
		return Record{}, false
	}
	r, err := decodeRecord(b[rel+4:rel+4+int(size)], t)
	return r, err == nil
}

// EachToolOutput streams every tool-output record in the store together with
// the session it came from.
//
// The alternative is loading each session by identity and reading its
// messages, which is what `deja friction` did first: 600 sessions took 2m46s,
// because every lookup walks the whole log. One pass over records.bin, keyed
// by the manifest, is the same data in a single scan.
func EachToolOutput(dir string, fn func(SessionMeta, Record)) error {
	return EachRecordOfRole(dir, roleToolOutput, fn)
}

// HasRecordOfRole reports whether the store holds any record of one role.
//
// An empty result under `--role tool` reads as "your query missed" when the
// truth can be "nothing here records that": a harness that writes only the talk
// gives a transcript that looks complete, and the reader has no way to tell the
// two apart (#1321). Called only when a role-filtered search came back empty, so
// it walks the records once on a path that has already returned nothing.
func HasRecordOfRole(dir, role string) bool {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return true // unknown is not the same as absent; say nothing extra
	}
	found := false
	// Stops at the first one. The note this feeds only prints when the answer is
	// no, and that case has to read the whole log either way — but the case that
	// prints nothing is the common one, and paying a full scan of a multi-gigabyte
	// log to stay silent is not a cost an empty search should carry.
	_ = eachRecordUntil(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) bool {
		if r.Role == role {
			found = true
		}
		return !found
	})
	return found
}

// betweenManifestAndRecords is the swap window, exposed so it can be entered
// on purpose. Racing a rebuild to reach it took 27,531 reads and hit it zero
// times; the window is real either way, and a test that cannot enter it pins
// nothing (#2627). nil outside tests.
var betweenManifestAndRecords func()

// manifestStamp identifies the generation of the store on disk. mtime and size
// rather than Manifest.Generation: it is the pair readManifestCached already
// keys its cache on and it needs no decode. It changes on almost any rewrite —
// a rewrite that keeps the size and lands inside one tick of the filesystem's
// timestamp resolution does not move it, which is why the writer drops the
// manifest cache rather than trusting this to notice.
func manifestStamp(dir string) string {
	fi, err := os.Stat(filepath.Join(dir, "manifest.gob"))
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d/%d", fi.ModTime().UnixNano(), fi.Size())
}

// walkRecordsStable runs a record walk and reports whether it read one
// generation of the store.
//
// A reader takes the manifest, then opens records.bin by path and resolves
// every record's key through the manifest it read. swapIndexDir can land
// between those two steps; swap_window.go covers the ENOENT that produces, but
// not this — a manifest from one generation against records from the next
// resolves only the sessions both hold. Measured on a 20-session store rebuilt
// to 60, the walk returned 20 records, no error, and nothing to tell the
// caller. This walk feeds `deja how`, restore's inventory and the "no record of
// that role" note, where a short answer reads as "this machine never did that"
// (#2627).
//
// Reported rather than retried: the caller has already been handed the records
// of the first pass, so walking again would hand it the store twice. Saying the
// read straddled a rebuild lets the caller ask again, which is the only safe
// version of the same idea.
//
// An incremental append is not that. It continues the interning table it found
// (appendIncremental takes tablesFromManifest(old)) and carries the manifest
// forward unchanged but for the new files, so the old manifest stays true of
// the log it was written against and a record for a session it does not know
// is skipped. That rewrites manifest.gob without being a straddle, which is why
// the generation decides rather than the stamp: a full build stamps a new one,
// an append keeps the old.
func walkRecordsStable(dir string, walk func(m Manifest) error) error {
	m, err := readManifestCached(dir)
	if err != nil {
		return err
	}
	before := manifestStamp(dir)
	if betweenManifestAndRecords != nil {
		betweenManifestAndRecords()
	}
	if walkErr := walk(m); walkErr != nil {
		return walkErr
	}
	if manifestStamp(dir) == before {
		return nil
	}
	after, err := readManifestCached(dir)
	if err != nil || after.Generation == m.Generation {
		return nil //nolint:nilerr // an unreadable manifest proves no rebuild
	}
	return errors.New("the index was rebuilt while this read was in flight — run it again")
}

// eachRecordOfRoles is EachRecordOfRole for more than one role at a time, in
// the order the records were written. A caller that ties a command to what it
// printed needs both roles in one pass: two passes would lose the order
// between them.
func eachRecordOfRoles(dir string, roles map[string]bool, fn func(SessionMeta, Record)) error {
	if dir == "" {
		dir = DefaultDir()
	}
	return walkRecordsStable(dir, func(m Manifest) error {
		return eachRecordInRoles(filepath.Join(dir, "records.bin"), tablesFromManifest(m), roles,
			func(r Record) {
				if meta, ok := m.Sessions[r.Key]; ok {
					fn(meta, r)
				}
			})
	})
}

// EachRecordOfRole streams every record of one role with the session it came
// from. Ranked retrieval is the wrong instrument when the caller has an exact
// key and needs every match rather than the best ones — restore is that case
// (#647), and so is any inventory of what the store holds.
func EachRecordOfRole(dir, role string, fn func(SessionMeta, Record)) error {
	if dir == "" {
		dir = DefaultDir()
	}
	return walkRecordsStable(dir, func(m Manifest) error {
		return eachRecordInRoles(filepath.Join(dir, "records.bin"), tablesFromManifest(m),
			map[string]bool{role: true}, func(r Record) {
				if meta, ok := m.Sessions[r.Key]; ok {
					fn(meta, r)
				}
			})
	})
}

// EachRecordInRoles is eachRecordOfRoles for callers outside the package: a
// session's turns of several kinds, in the order they alternated.
func EachRecordInRoles(dir string, roles []string, fn func(SessionMeta, Record)) error {
	want := make(map[string]bool, len(roles))
	for _, r := range roles {
		want[r] = true
	}
	return eachRecordOfRoles(dir, want, fn)
}

// RoleEdit is the record kind holding the exact bytes an agent replaced.
const RoleEdit = roleEdit

// SpanInventory counts the replaced spans the store holds and the files they
// belong to.
//
// It needs its own pass: a span is served only when asked for by role, because
// it is the file's old contents rather than a statement about anything, so it
// never reaches a caller loading sessions the ordinary way.
//
// This is what `deja restore` has to hand back, stated as a number before
// anyone needs it — the command matters entirely at one panicked moment, and
// nobody reads a command list then (#577). Measured at ~180 ms on a
// 160k-record store, so it belongs on a page someone opens deliberately.
func SpanInventory(dir string) (spans, files int, err error) {
	return SpanInventoryWhere(dir, nil)
}

// SpanInventoryWhere is SpanInventory over the sessions keep accepts, for a
// report narrowed to a project, a harness or a window. A nil keep takes all.
func SpanInventoryWhere(dir string, keep func(SessionMeta) bool) (spans, files int, err error) {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return 0, 0, err
	}
	seen := map[string]bool{}
	pol := policy.Load()
	excluded := excludedHarnesses()
	err = eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
		if r.Role != roleEdit {
			return
		}
		// What this machine could hand back, not what the log happens to hold:
		// `restore` refuses a span from a tree the ignore rule covers (#2630),
		// so counting it here promised something the command would decline
		// (#2650).
		meta, ok := m.Sessions[r.Key]
		if !ok || metaIgnoredOrExcluded(pol, excluded, meta) || (keep != nil && !keep(meta)) {
			return
		}
		spans++
		// The path is the first line of a span; one without a path still
		// counts as something recoverable.
		if i := strings.IndexByte(r.Text, '\n'); i > 0 {
			seen[r.Text[:i]] = true
		}
	})
	if err != nil {
		return 0, 0, err
	}
	return spans, len(seen), nil
}
