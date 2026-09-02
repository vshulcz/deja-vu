package index

import (
	"bufio"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/vshulcz/deja-vu/internal/policy"
)

// recordLogScans counts walks of the record log. There are no per-session
// offsets, so every walk reads the whole of records.bin — which is why a
// caller asking for sessions one at a time is a cost bug and not a style
// point (#1069). Exported through RecordLogScans for tests that assert a
// batch path stayed one pass.
var recordLogScans int64

// RecordLogScans reports how many times this process walked the record log.
func RecordLogScans() int64 { return atomic.LoadInt64(&recordLogScans) }

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
	for {
		off, err := f.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, err
		}
		r, err := readRecord(f, t)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		out = append(out, OffsetRecord{Offset: off, Record: r})
	}
	return out, nil
}

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
		return 0, fmt.Errorf("record too large")
	}
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(b)))
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
		return 0, fmt.Errorf("record too large")
	}
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := f.Write(hdr[:]); err != nil {
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
	buf := readAheadPool.Get().(*[]byte)
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
	if _, err := f.ReadAt(payload, off+4); err != nil && !errors.Is(err, io.EOF) {
		return Record{}, err
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
	r := bufio.NewReaderSize(f, 1024*1024)
	for {
		rec, err := readRecord(r, t)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}
		if err != nil {
			return err
		}
		if !fn(rec) {
			return nil
		}
	}
}

func eachRecord(path string, t *recordTables, fn func(Record)) error {
	f, err := openIndexFile(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReaderSize(f, 1024*1024)
	for {
		rec, err := readRecord(r, t)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}
		if err != nil {
			return err
		}
		fn(rec)
	}
}

func readRecord(r io.Reader, t *recordTables) (Record, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Record{}, err
	}
	n := binary.LittleEndian.Uint32(hdr[:])
	if n > maxRecordSize {
		return Record{}, fmt.Errorf("%w: record length %d exceeds cap", errCorruptIndex, n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return Record{}, err
	}
	return decodeRecord(b, t)
}

// eachRecordForKeys walks the log decoding only records whose Key is in
// want; other bodies are skipped after peeking the key field. On a large log
// this trades a full decode of every record for a few length reads.
func eachRecordForKeys(path string, t *recordTables, want map[string]bool, fn func(Record)) error {
	atomic.AddInt64(&recordLogScans, 1)
	f, err := openIndexFile(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReaderSize(f, 1024*1024)
	var hdr [4]byte
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
		peek := int(n)
		if peek > binary.MaxVarintLen64 {
			peek = binary.MaxVarintLen64
		}
		head, perr := r.Peek(peek)
		if perr != nil && len(head) == 0 {
			if perr == io.EOF || perr == io.ErrUnexpectedEOF {
				return nil
			}
			return perr
		}
		kid, un := binary.Uvarint(head)
		if un <= 0 || !want[t.lookup(kid)] {
			if _, derr := r.Discard(int(n)); derr != nil {
				if derr == io.EOF || derr == io.ErrUnexpectedEOF {
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
			return fmt.Errorf("%w: %v", errCorruptIndex, derr)
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

func tablesFromManifest(m Manifest) *recordTables { return tablesFromStrings(m.RecordStrings) }

func loadRecordTables(dir string) (*recordTables, error) {
	// Cached: this sits on the search path, and re-decoding the whole
	// manifest per query is what the interning was meant to avoid paying for.
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, err
	}
	return tablesFromManifest(m), nil
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

// compressFloor is deliberately high. Compressing every message halved the
// record log but doubled index build time and tripled the latency of a query
// that materializes many records — the payload is touched once per message on
// write and once per message shown. The mass is in the tail instead: on a real
// corpus 1.1% of messages (tool output, file dumps) hold 22.6% of the text, so
// only those are worth the CPU.
const compressFloor = 8192

const (
	recordRaw      = 0
	recordDeflated = 1
)

var deflateWriters = sync.Pool{New: func() any { w, _ := flate.NewWriter(nil, flate.DefaultCompression); return w }}
var inflateReaders = sync.Pool{New: func() any { return flate.NewReader(nil) }}

// A record is [keyID][pathID][flag][payload]. The ids stay outside the
// payload so a walk can peek the key and skip the record without inflating
// anything — that path never touches a compressed byte.
func encodeRecord(r Record, t *recordTables) []byte {
	body := make([]byte, 0, len(r.Role)+len(r.Text)+24)
	body = appendField(body, r.Role)
	body = binary.LittleEndian.AppendUint64(body, uint64(recordNanos(r.Time)))
	body = appendField(body, r.Text)

	b := make([]byte, 0, len(body)+16)
	b = binary.AppendUvarint(b, t.intern(r.Key))
	b = binary.AppendUvarint(b, t.intern(r.SourcePath))
	if len(body) < compressFloor {
		return append(append(b, recordRaw), body...)
	}
	var buf bytes.Buffer
	w := deflateWriters.Get().(*flate.Writer)
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

func decodeRecord(b []byte, t *recordTables) (Record, error) {
	var rec Record
	var ok bool
	kid, n := binary.Uvarint(b)
	if n <= 0 {
		return rec, io.ErrUnexpectedEOF
	}
	b = b[n:]
	pid, n := binary.Uvarint(b)
	if n <= 0 {
		return rec, io.ErrUnexpectedEOF
	}
	b = b[n:]
	rec.Key = t.lookup(kid)
	rec.SourcePath = t.lookup(pid)
	if len(b) < 1 {
		return rec, io.ErrUnexpectedEOF
	}
	flag := b[0]
	b = b[1:]
	if flag == recordDeflated {
		zr := inflateReaders.Get().(io.ReadCloser)
		if err := zr.(flate.Resetter).Reset(bytes.NewReader(b), nil); err != nil {
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
	if rec.Role, b, ok = consumeField(b); !ok {
		return rec, io.ErrUnexpectedEOF
	}
	if len(b) < 8 {
		return rec, io.ErrUnexpectedEOF
	}
	// time.Time{}.UnixNano() is a large negative number that time.Unix turns
	// into the year 1754, so an unstamped message never satisfied IsZero()
	// again. Sync then read it as older than any watermark and skipped it on
	// every push — the message could never reach another machine, silently.
	if n := int64(binary.LittleEndian.Uint64(b[:8])); n == zeroTimeUnixNano {
		rec.Time = time.Time{}
	} else {
		rec.Time = time.Unix(0, n)
	}
	b = b[8:]
	if rec.Text, _, ok = consumeField(b); !ok {
		return rec, io.ErrUnexpectedEOF
	}
	return rec, nil
}

func consumeField(b []byte) (string, []byte, bool) {
	n, used := binary.Uvarint(b)
	if used <= 0 || uint64(len(b)-used) < n {
		return "", nil, false
	}
	start := used
	end := start + int(n)
	return string(b[start:end]), b[end:], true
}

// swapIndexDir replaces dir with tmp without a destructive window: the old
// dir is parked as dir.old until the new one is in place, so a crash between
// steps leaves a recoverable copy instead of nothing (#181).
func swapIndexDir(dir, tmp string) error {
	old := dir + ".old"
	_ = os.RemoveAll(old)
	// The parking spot survived the removal above — a leftover from an
	// interrupted swap whose permissions stop deja from clearing it. The bare
	// `rename …: file exists` names two paths nobody chose and gives nothing to
	// do about either (#1009). Decided before the wait: no amount of waiting
	// clears it, and the reader has to act on the message either way.
	if _, statErr := os.Stat(old); statErr == nil {
		return fmt.Errorf("an earlier index swap left %s behind and deja cannot replace it — remove that directory and run `deja index` again", old)
	}
	// The parking step can afford to wait: the index is still where readers
	// look until it succeeds, so nobody is waiting on it, and this is the
	// rename a pass that spent seconds building tmp would otherwise give up on
	// for the sake of a search that held a handle for a quarter of a second.
	if err := renameWaiting(dir, old, parkRenameWait); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// From here the index is not where readers look, so this rename and the
	// restore that may follow it share one deadline: the window a reader waits
	// out, spent once between them rather than twice.
	deadline := time.Now().Add(swapRenameWait)
	if err := renameWaitingUntil(tmp, dir, deadline); err != nil {
		// Put the previous index back rather than leaving nothing, with
		// whatever is left of that window — this runs exactly when renames on
		// this directory are being refused.
		floor := time.Now().Add(restoreRenameFloor)
		if floor.After(deadline) {
			// A little past the window rather than none of it: the restore is
			// the one rename that must not fail, and a second rename that
			// spent the whole budget would otherwise leave it none.
			deadline = floor
		}
		_ = renameWaitingUntil(old, dir, deadline)
		return err
	}
	_ = os.RemoveAll(old)
	return nil
}

// renameWaiting is os.Rename with a short wait for a rename another pass is
// holding the directory open against.
//
// Windows refuses to rename a directory while any handle inside it is open, so
// two ordinary passes at once — a hook's warmup and a `deja index` from a shell
// — left the loser unable to swap, and the store a session short until
// something rebuilt it. On Unix the loser renames over the winner and both end
// up consistent, which is why this went unmeasured (#2228). The reader holding
// those handles is finishing a read, not doing work, so the wait is short and
// bounded; a rename still refused at the end is reported rather than retried
// forever, and the caller puts the previous index back.
func renameWaiting(from, to string, wait time.Duration) error {
	return renameWaitingUntil(from, to, time.Now().Add(wait))
}

// renameWaitingUntil is renameWaiting against a deadline the caller keeps, so a
// swap and the restore that follows it share one window rather than each
// getting a fresh one — two full budgets in a row is the index away for twice
// as long as a reader will wait (#2228).
func renameWaitingUntil(from, to string, deadline time.Time) error {
	err := renameFile(from, to)
	for err != nil && renameHeldOpen(err) && time.Now().Before(deadline) {
		time.Sleep(swapRenameStep)
		err = renameFile(from, to)
	}
	return err
}

// renameHeldOpen reports whether a refusal is the kind that clears on its own.
// A handle in the directory is; a read-only filesystem, a path that is a file,
// a parking spot deja cannot replace (#1009) are not, and waiting two seconds
// to say so delays a message the reader has to act on either way.
//
// By errno rather than by fs.ErrPermission, which does not discriminate here:
// Windows reports both a sharing refusal and a real ACL denial as
// ERROR_ACCESS_DENIED.
func renameHeldOpen(err error) bool {
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch uintptr(errno) {
	case 5, 32, 33: // ACCESS_DENIED, SHARING_VIOLATION, LOCK_VIOLATION
		// Those numbers mean something else on Unix, where the rename does not
		// fail this way at all, so the wait is Windows' alone. A variable
		// rather than runtime.GOOS so a test can stand where Windows is.
		return renameOS == "windows"
	}
	return false
}

// renameOS is runtime.GOOS, indirected for the tests that inject a refusal.
var renameOS = runtime.GOOS

const (
	// swapRenameWait is how long a swap waits out a reader, and it is bounded
	// by what a reader will wait for it: swapWindowTries × swapWindowWait is
	// 200ms, after which a reader stops looking and gets the bare ENOENT that
	// swapInFlight exists to absorb. Waiting longer than that turns one pass
	// losing its swap into every reader losing (#2228).
	//
	// The same refusal atomicfile.publish waits out, measured on windows CI at
	// 20 × 5ms; this is the same order, in the steps the reader side uses.
	swapRenameWait = swapWindowTries * swapWindowWait
	swapRenameStep = swapWindowWait
	// parkRenameWait is the other side of that: the parking step and the
	// crash-recovery restore happen while the index is still (or already)
	// where readers look, so nothing is waiting on them and the only cost of
	// waiting is the pass's own. Long enough to outlast a search holding a
	// handle, short enough that a directory nobody will ever release still
	// reports inside a scheduled run.
	parkRenameWait = 2 * time.Second
	// restoreRenameFloor is what the restore gets when the rename before it
	// spent the shared window. Three attempts, which is what the refusal this
	// waits out clears in.
	restoreRenameFloor = 3 * swapRenameStep
)

// renameFile is os.Rename, indirected so a test can refuse a rename the way
// Windows does without needing Windows.
var renameFile = os.Rename

// recoverIndexDir finishes an interrupted swapIndexDir: if the index dir is
// missing but its .old sibling survives, restore it.
func recoverIndexDir(dir string) {
	if dir == "" {
		return
	}
	if _, err := os.Stat(dir); err == nil {
		_ = os.RemoveAll(dir + ".old")
		return
	}
	if _, err := os.Stat(dir + ".old"); err == nil {
		// The same wait: this is the crash-recovery path, and a refusal here
		// leaves the index missing for the whole run.
		_ = renameWaiting(dir+".old", dir, parkRenameWait)
	}
}

func writeBucket(p string, data map[string][]posting) error {
	toks := make([]string, 0, len(data))
	for tok := range data {
		toks = append(toks, tok)
	}
	sort.Strings(toks)
	// Posting blocks are written back to back in directory order, so an
	// entry's offset is the running sum of the lengths before it. Storing it
	// cost 8 bytes per token — 180k tokens on a real corpus — to record a
	// number the reader can add up itself.
	encoded := make(map[string][]byte, len(toks))
	dirLen := len(bucketMagic) + uvarintLen(uint64(len(toks)))
	for _, tok := range toks {
		b := encodePostings(data[tok])
		encoded[tok] = b
		dirLen += uvarintLen(uint64(len(tok))) + len(tok) + uvarintLen(uint64(len(b)))
	}
	entries := make([]bucketEntry, 0, len(toks))
	pos := uint64(dirLen)
	for _, tok := range toks {
		b := encoded[tok]
		entries = append(entries, bucketEntry{tok: tok, off: pos, n: uint32(len(b))})
		pos += uint64(len(b))
	}
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w := bufio.NewWriterSize(f, 1<<20)
	if _, err := w.Write(bucketMagic); err != nil {
		return err
	}
	var scratch [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(scratch[:], uint64(len(entries)))
	if _, err := w.Write(scratch[:n]); err != nil {
		return err
	}
	for _, e := range entries {
		n = binary.PutUvarint(scratch[:], uint64(len(e.tok)))
		if _, err := w.Write(scratch[:n]); err != nil {
			return err
		}
		if _, err := w.Write([]byte(e.tok)); err != nil {
			return err
		}
		n = binary.PutUvarint(scratch[:], uint64(e.n))
		if _, err := w.Write(scratch[:n]); err != nil {
			return err
		}
	}
	for _, tok := range toks {
		if _, err := w.Write(encoded[tok]); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Rename over the live bucket: readers see the old file or the new one,
	// never a torn write (#181).
	return os.Rename(tmp, p)
}

func readBucket(p string) (map[string][]posting, error) {
	entries, f, err := openBucketDir(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := make(map[string][]posting, len(entries))
	for _, e := range entries {
		b := make([]byte, e.n)
		if _, err := f.ReadAt(b, int64(e.off)); err != nil {
			return nil, err
		}
		out[e.tok] = decodePostings(b)
	}
	return out, nil
}

func readBucketToken(p, tok string) ([]posting, error) {
	entries, f, err := openBucketDir(p)
	if err != nil {
		// A bucket that was never written means the token simply does not
		// occur — that is "no postings", not a failure. Erroring here made
		// the stem tier abort whole searches over an absent shard.
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	for _, e := range entries {
		if e.tok != tok {
			continue
		}
		b := make([]byte, e.n)
		if _, err := f.ReadAt(b, int64(e.off)); err != nil {
			return nil, err
		}
		return decodePostings(b), nil
	}
	return nil, nil
}

func openBucketDir(p string) ([]bucketEntry, *os.File, error) {
	f, err := openIndexFile(p)
	if err != nil {
		return nil, nil, err
	}
	r := bufio.NewReader(f)
	magic := make([]byte, len(bucketMagic))
	if _, err := io.ReadFull(r, magic); err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("%w: %v", errCorruptIndex, err)
	}
	if string(magic) != string(bucketMagic) {
		f.Close()
		// Named for what it is. The magic moves when the posting format
		// changes, and every reader of an index written by another version
		// lands here — telling them the index is damaged sends them looking
		// for a disk fault they do not have (#492).
		return nil, nil, fmt.Errorf("%w: %s", errCorruptIndex, wrongBucketFormat(magic))
	}
	count, err := binary.ReadUvarint(r)
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("%w: %v", errCorruptIndex, err)
	}
	// count and the token lengths below come straight off disk. A truncated
	// file, or one written by a different index layout, can name a size no
	// allocation could satisfy — and make() panics on that rather than
	// failing. Every other corruption here is recoverable, so bound both
	// against the file: no entry occupies fewer than two bytes.
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	fileSize := uint64(fi.Size())
	if count > fileSize/2 {
		f.Close()
		return nil, nil, fmt.Errorf("%w: bucket directory claims %d entries in %d bytes", errCorruptIndex, count, fileSize)
	}
	entries := make([]bucketEntry, 0, count)
	// Offsets are not stored; they are the running sum of the lengths, taken
	// from where the directory ends.
	dirLen := uint64(len(bucketMagic)) + uint64(uvarintLen(count))
	for i := uint64(0); i < count; i++ {
		ln, err := binary.ReadUvarint(r)
		if err != nil {
			f.Close()
			return nil, nil, fmt.Errorf("%w: %v", errCorruptIndex, err)
		}
		if ln > fileSize {
			f.Close()
			return nil, nil, fmt.Errorf("%w: token length %d exceeds bucket size %d", errCorruptIndex, ln, fileSize)
		}
		tb := make([]byte, ln)
		if _, err := io.ReadFull(r, tb); err != nil {
			f.Close()
			return nil, nil, fmt.Errorf("%w: %v", errCorruptIndex, err)
		}
		size, err := binary.ReadUvarint(r)
		if err != nil {
			f.Close()
			return nil, nil, fmt.Errorf("%w: %v", errCorruptIndex, err)
		}
		// MaxUint32 alone is not a bound: a bucket claiming four gigabytes for
		// one token is an out-of-memory kill rather than a corrupt-index
		// error, and the caller can recover from the latter. Postings live in
		// this same file, so the file's own size is the real ceiling.
		if size > math.MaxUint32 || size > fileSize {
			f.Close()
			return nil, nil, fmt.Errorf("%w: posting block of %d bytes in a %d byte bucket", errCorruptIndex, size, fileSize)
		}
		dirLen += uint64(uvarintLen(ln)) + ln + uint64(uvarintLen(size))
		entries = append(entries, bucketEntry{tok: string(tb), n: uint32(size)})
	}
	pos := dirLen
	for i := range entries {
		entries[i].off = pos
		pos += uint64(entries[i].n)
	}
	return entries, f, nil
}

// damagedOrOutdated names the two reasons an index cannot be read. A format
// this build does not write is not damage, and saying so sent a reader looking
// for a disk fault (#492).
func damagedOrOutdated(err error) string {
	if err != nil && strings.Contains(err.Error(), "bucket format") {
		return "index written by another version of deja"
	}
	return "index damaged"
}

// wrongBucketFormat says which of the two shapes a bucket file is in, so the
// message a reader gets names the version rather than a fault.
func wrongBucketFormat(magic []byte) string {
	// Only a magic deja itself has written means another version. Anything
	// else is a header that is not one, which is damage — and calling that a
	// version difference would send a reader to the release notes for a disk
	// fault.
	for _, known := range formerBucketMagics {
		if string(magic) == known {
			return fmt.Sprintf("postings written by an older deja (bucket format %s, this build reads %s)",
				known, string(bucketMagic))
		}
	}
	return fmt.Sprintf("bad bucket magic %q", printableMagic(magic))
}

// formerBucketMagics are the bucket headers earlier releases wrote. A reader
// that meets one is behind or ahead of a format change rather than looking at
// a damaged file.
var formerBucketMagics = []string{"DJB1"}

// printableMagic keeps a corrupt header out of the terminal as raw bytes.
func printableMagic(magic []byte) string {
	out := make([]rune, 0, len(magic))
	for _, b := range magic {
		if b < 0x20 || b > 0x7e {
			out = append(out, '?')
			continue
		}
		out = append(out, rune(b))
	}
	return string(out)
}

// encodePostings encodes a posting block with per-block offset and session
// deltas.
func encodePostings(posts []posting) []byte {
	if len(posts) == 0 {
		return nil
	}
	s := sortedUniquePostings(posts)
	b := make([]byte, 0, len(s)*6)
	var prev int64
	var prevSid uint32
	for _, p := range s {
		b = binary.AppendUvarint(b, uint64(p.Off-prev))
		// A block is sorted by offset and records.bin is written in session
		// order, so the session ids in one block are nearly sorted: 99.66% of
		// postings on a real store carry an id no smaller than the one before
		// them, and writing the difference instead of the id took 16% off the
		// buckets (#492). Zigzag because "nearly" is not "always" — the ids
		// that step backwards must survive too.
		//
		// The subtraction is deliberately modulo 2^32, which is what int32 of
		// a uint32 difference gives: it round-trips the whole id space rather
		// than the half an int32 could hold.
		d := int32(p.Sid - prevSid)
		z := uint32((d << 1) ^ (d >> 31))
		// The tool bit rides in the low bit, where it has always ridden: a
		// separate varint would cost a byte on every posting in the store.
		v := uint64(z) << 1
		if p.Tool {
			v |= 1
		}
		b = binary.AppendUvarint(b, v)
		prev = p.Off
		prevSid = p.Sid
	}
	return b
}

// decodePostings mirrors encodePostings. A truncated varint ends the walk and
// yields what was whole rather than panicking.
//
// Defensive only: every caller sizes its buffer from the directory entry and
// gets io.EOF from ReadAt before a short block reaches here, and openBucketDir
// bounds each block against the file size. Nothing in the product depends on
// the prefix coming back — an earlier version of this comment claimed two
// callers did, and none does.
func decodePostings(b []byte) []posting {
	out := make([]posting, 0)
	var prev int64
	var prevSid uint32
	for len(b) > 0 {
		d, n := binary.Uvarint(b)
		if n <= 0 {
			return out
		}
		prev += int64(d)
		b = b[n:]
		v, n := binary.Uvarint(b)
		if n <= 0 {
			return out
		}
		z := uint32(v >> 1)
		sidDelta := int32(z>>1) ^ -int32(z&1)
		sid := prevSid + uint32(sidDelta)
		out = append(out, posting{Off: prev, Sid: sid, Tool: v&1 == 1})
		prevSid = sid
		b = b[n:]
	}
	return out
}

func uvarintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

func writeGob(p string, v any) error {
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return gob.NewEncoder(f).Encode(v)
}

// writeGobAtomic writes to a sibling temp file and renames it over p, so a
// crash mid-write can never leave p half-decoded.
func writeGobAtomic(p string, v any) error {
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := gob.NewEncoder(f).Encode(v); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	// fsync before rename: manifest.gob is the freshness/RecordsSize authority and
	// must land durably last, like records.bin and the buckets. Skipping it left a
	// window where a crash after rename but before flush yields a torn manifest.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, p)
}

func readGob(p string, v any) error {
	f, err := openIndexFile(p)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := gob.NewDecoder(f).Decode(v); err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(p), err)
	}
	return nil
}

// bucketReader memoizes parsed bucket directories for the span of one query.
// A bucket's directory lists every token in that shard, and readBucketToken
// re-read and re-parsed the whole thing per token. The relevance tier asks
// for every inflected form of a term, and those forms share their opening
// runes — so they all hash to the same bucket and re-parsed the same
// directory dozens of times per query. Only the directory is cached; the
// file is reopened per read so a concurrent index swap is never blocked by a
// held handle.
type bucketReader struct {
	dir     string
	entries map[string][]bucketEntry
}

func newBucketReader(dir string) *bucketReader {
	return &bucketReader{dir: dir, entries: map[string][]bucketEntry{}}
}

func (b *bucketReader) postings(tok string) ([]posting, error) {
	p := filepath.Join(b.dir, "buckets", bucket(tok)+".bin")
	entries, cached := b.entries[p]
	if !cached {
		e, f, err := openBucketDir(p)
		if err != nil {
			// A bucket that was never written means the token does not occur
			// — "no postings", not a failure.
			if os.IsNotExist(err) {
				b.entries[p] = nil
				return nil, nil
			}
			return nil, err
		}
		_ = f.Close()
		b.entries[p] = e
		entries = e
	}
	for _, e := range entries {
		if e.tok != tok {
			continue
		}
		f, err := openIndexFile(p)
		if err != nil {
			return nil, err
		}
		buf := make([]byte, e.n)
		_, err = f.ReadAt(buf, int64(e.off))
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		return decodePostings(buf), nil
	}
	return nil, nil
}

func (b *bucketReader) close() { b.entries = nil }

// Coalescing bounds for eachRecordAt. A search over a common term resolves a
// few thousand records that sit close together in write order — the median hole
// between two of them is under 3 KB — so reading each one separately pays a
// pread per record for data the kernel would have handed over in the same page.
const (
	spanGapMax  = 16 * 1024  // join across a hole no larger than this
	spanSizeMax = 512 * 1024 // and never read more than this at once
)

// eachRecordAt reads records at the given offsets, which must be sorted, and
// calls fn for each one it could decode. Offsets that turn out to reach past
// the span read for them fall back to a single read, so a long record costs
// what it always did rather than forcing the span to grow.
func eachRecordAt(f *os.File, offsets []int64, t *recordTables, fn func(Record)) error {
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
			rel := int(off - start)
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
// keys its cache on, it needs no decode, and it changes on any rewrite.
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
	if err := walk(m); err != nil {
		return err
	}
	if manifestStamp(dir) == before {
		return nil
	}
	after, err := readManifestCached(dir)
	if err != nil || after.Generation == m.Generation {
		return nil
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
		return eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
			if !roles[r.Role] {
				return
			}
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
		return eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
			if r.Role != role {
				return
			}
			if meta, ok := m.Sessions[r.Key]; ok {
				fn(meta, r)
			}
		})
	})
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
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return 0, 0, err
	}
	seen := map[string]bool{}
	pol := policy.Load()
	err = eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
		if r.Role != roleEdit {
			return
		}
		// What this machine could hand back, not what the log happens to hold:
		// `restore` refuses a span from a tree the ignore rule covers (#2630),
		// so counting it here promised something the command would decline
		// (#2650).
		meta, ok := m.Sessions[r.Key]
		if !ok || pol.Ignored(meta.Path, meta.Project) {
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
