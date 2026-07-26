package index

import (
	"bufio"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

func ReadRecords(dir string) ([]OffsetRecord, error) {
	t, err := loadRecordTables(dir)
	if err != nil {
		return nil, err
	}
	var out []OffsetRecord
	f, err := os.Open(filepath.Join(dir, "records.bin"))
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
	if m.Generation != "" {
		return m.Generation, nil
	}
	return m.BuiltAt.UTC().Format(time.RFC3339Nano), nil
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

func readRecordAt(f *os.File, off int64, t *recordTables) (Record, error) {
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return Record{}, err
	}
	return readRecord(f, t)
}

func eachRecord(path string, t *recordTables, fn func(Record)) error {
	f, err := os.Open(path)
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
	f, err := os.Open(path)
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
			continue
		}
		fn(rec)
	}
}

// zeroTimeUnixNano is what the zero time serializes to; records.bin has
// always stored it, so it stays the on-disk marker for "never stamped".
var zeroTimeUnixNano = time.Time{}.UnixNano()

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
	body = binary.LittleEndian.AppendUint64(body, uint64(r.Time.UnixNano()))
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
	if err := os.Rename(dir, old); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		// Put the previous index back rather than leaving nothing.
		_ = os.Rename(old, dir)
		return err
	}
	_ = os.RemoveAll(old)
	return nil
}

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
		_ = os.Rename(dir+".old", dir)
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
	f, err := os.Open(p)
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
		return nil, nil, fmt.Errorf("%w: bad bucket magic", errCorruptIndex)
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
		if size > math.MaxUint32 {
			f.Close()
			return nil, nil, fmt.Errorf("%w: posting block length %d", errCorruptIndex, size)
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

func encodePostings(posts []posting) []byte {
	if len(posts) == 0 {
		return nil
	}
	s := sortedUniquePostings(posts)
	b := make([]byte, 0, len(s)*6)
	var prev int64
	for _, p := range s {
		b = binary.AppendUvarint(b, uint64(p.Off-prev))
		b = binary.AppendUvarint(b, uint64(p.Sid))
		prev = p.Off
	}
	return b
}

func decodePostings(b []byte) []posting {
	out := make([]posting, 0)
	var prev int64
	for len(b) > 0 {
		d, n := binary.Uvarint(b)
		if n <= 0 {
			return out
		}
		prev += int64(d)
		b = b[n:]
		sid, n := binary.Uvarint(b)
		if n <= 0 {
			return out
		}
		out = append(out, posting{Off: prev, Sid: uint32(sid)})
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
	f, err := os.Open(p)
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
		f, err := os.Open(p)
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
