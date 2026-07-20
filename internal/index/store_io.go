package index

import (
	"bufio"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func ReadRecords(dir string) ([]OffsetRecord, error) {
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
		r, err := readRecord(f)
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

func newRecordWriter(f *os.File) (*recordWriter, error) {
	off, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	return &recordWriter{f: f, w: bufio.NewWriterSize(f, 1<<20), off: off}, nil
}

func (rw *recordWriter) write(r Record) (int64, error) {
	b := encodeRecord(r)
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

func writeRecord(f *os.File, r Record) (int64, error) {
	off, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	b := encodeRecord(r)
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

func readRecordAt(f *os.File, off int64) (Record, error) {
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return Record{}, err
	}
	return readRecord(f)
}

func eachRecord(path string, fn func(Record)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReaderSize(f, 1024*1024)
	for {
		rec, err := readRecord(r)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}
		if err != nil {
			return err
		}
		fn(rec)
	}
}

func readRecord(r io.Reader) (Record, error) {
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
	return decodeRecord(b)
}

func encodeRecord(r Record) []byte {
	b := make([]byte, 0, len(r.Key)+len(r.SourcePath)+len(r.Role)+len(r.Text)+32)
	b = appendField(b, r.Key)
	b = appendField(b, r.SourcePath)
	b = appendField(b, r.Role)
	b = binary.LittleEndian.AppendUint64(b, uint64(r.Time.UnixNano()))
	b = appendField(b, r.Text)
	return b
}

func appendField(b []byte, s string) []byte {
	b = binary.AppendUvarint(b, uint64(len(s)))
	return append(b, s...)
}

func decodeRecord(b []byte) (Record, error) {
	var rec Record
	var ok bool
	if rec.Key, b, ok = consumeField(b); !ok {
		return rec, io.ErrUnexpectedEOF
	}
	if rec.SourcePath, b, ok = consumeField(b); !ok {
		return rec, io.ErrUnexpectedEOF
	}
	if rec.Role, b, ok = consumeField(b); !ok {
		return rec, io.ErrUnexpectedEOF
	}
	if len(b) < 8 {
		return rec, io.ErrUnexpectedEOF
	}
	rec.Time = time.Unix(0, int64(binary.LittleEndian.Uint64(b[:8])))
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
	encoded := make(map[string][]byte, len(toks))
	dirLen := len(bucketMagic) + uvarintLen(uint64(len(toks)))
	for _, tok := range toks {
		dirLen += uvarintLen(uint64(len(tok))) + len(tok) + 8 + 4
		encoded[tok] = encodePostings(data[tok])
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
		var fixed [12]byte
		binary.LittleEndian.PutUint64(fixed[:8], e.off)
		binary.LittleEndian.PutUint32(fixed[8:], e.n)
		if _, err := w.Write(fixed[:]); err != nil {
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
	entries := make([]bucketEntry, 0, count)
	for i := uint64(0); i < count; i++ {
		ln, err := binary.ReadUvarint(r)
		if err != nil {
			f.Close()
			return nil, nil, fmt.Errorf("%w: %v", errCorruptIndex, err)
		}
		tb := make([]byte, ln)
		if _, err := io.ReadFull(r, tb); err != nil {
			f.Close()
			return nil, nil, fmt.Errorf("%w: %v", errCorruptIndex, err)
		}
		var fixed [12]byte
		if _, err := io.ReadFull(r, fixed[:]); err != nil {
			f.Close()
			return nil, nil, fmt.Errorf("%w: %v", errCorruptIndex, err)
		}
		entries = append(entries, bucketEntry{tok: string(tb), off: binary.LittleEndian.Uint64(fixed[:8]), n: binary.LittleEndian.Uint32(fixed[8:])})
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
