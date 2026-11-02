package index

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
)

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
	pos := uint64(dirLen) //nolint:gosec // lengths are non-negative
	for _, tok := range toks {
		b := encoded[tok]
		entries = append(entries, bucketEntry{tok: tok, off: pos, n: uint32(len(b))}) //nolint:gosec // a token's postings are far below 4 GiB
		pos += uint64(len(b))
	}
	tmp := p + ".tmp"
	f, err := openIndexFileWrite(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC)
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
		if _, err := w.WriteString(e.tok); err != nil {
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
	// never a torn write (#181). Waiting out a reader that holds it open, as
	// Windows refuses the rename until it lets go.
	return renameWaiting(tmp, p)
}

// readBucketFile reads one postings file whole.
func readBucketFile(p string) (map[string][]posting, error) {
	entries, f, err := openBucketDir(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := make(map[string][]posting, len(entries))
	for _, e := range entries {
		b := make([]byte, e.n)
		if _, err := f.ReadAt(b, int64(e.off)); err != nil { //nolint:gosec // offsets are checked against the bucket size on open
			return nil, err
		}
		posts, err := decodePostingBlock(b)
		if err != nil {
			return nil, err
		}
		out[e.tok] = posts
	}
	return out, nil
}

// readBucketFileToken reads one token's postings from one postings file.
func readBucketFileToken(p, tok string) ([]posting, error) {
	f, err := openIndexFile(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	d, err := bucketDirOf(p, f)
	if err != nil {
		return nil, err
	}
	e, ok := d.find(tok)
	if !ok {
		return nil, nil
	}
	b := make([]byte, e.n)
	if _, err := f.ReadAt(b, int64(e.off)); err != nil { //nolint:gosec // offsets are checked against the bucket size on open
		return nil, err
	}
	return decodePostingBlock(b)
}

// bucketDirReads counts bucket directories read off disk, for the test that
// pins the part-of-word step to the catalog's copy of them.
var bucketDirReads atomic.Int64

func openBucketDir(p string) ([]bucketEntry, *os.File, error) {
	f, err := openIndexFile(p)
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	entries, err := readBucketDir(f, fi)
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return entries, f, nil
}

// readBucketDir parses the directory of the bucket file f, just opened, which
// fi describes. f is left open.
func readBucketDir(f *os.File, fi os.FileInfo) ([]bucketEntry, error) {
	bucketDirReads.Add(1)
	r := bufio.NewReader(f)
	magic := make([]byte, len(bucketMagic))
	if _, err := io.ReadFull(r, magic); err != nil {
		return nil, fmt.Errorf("%w: %w", errCorruptIndex, err)
	}
	if !bytes.Equal(magic, bucketMagic) {
		// Named for what it is. The magic moves when the posting format
		// changes, and every reader of an index written by another version
		// lands here — telling them the index is damaged sends them looking
		// for a disk fault they do not have (#492).
		return nil, fmt.Errorf("%w: %s", errCorruptIndex, wrongBucketFormat(magic))
	}
	count, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errCorruptIndex, err)
	}
	// count and the token lengths below come straight off disk. A truncated
	// file, or one written by a different index layout, can name a size no
	// allocation could satisfy — and make() panics on that rather than
	// failing. Every other corruption here is recoverable, so bound both
	// against the file: no entry occupies fewer than two bytes.
	fileSize := uint64(fi.Size()) //nolint:gosec // file sizes are non-negative
	if count > fileSize/2 {
		return nil, fmt.Errorf("%w: bucket directory claims %d entries in %d bytes", errCorruptIndex, count, fileSize)
	}
	entries := make([]bucketEntry, 0, count)
	// Offsets are not stored; they are the running sum of the lengths, taken
	// from where the directory ends.
	dirLen := uint64(len(bucketMagic)) + uint64(uvarintLen(count)) //nolint:gosec // lengths are non-negative
	for range count {
		ln, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errCorruptIndex, err)
		}
		if ln > fileSize {
			return nil, fmt.Errorf("%w: token length %d exceeds bucket size %d", errCorruptIndex, ln, fileSize)
		}
		tb := make([]byte, ln)
		if _, err = io.ReadFull(r, tb); err != nil {
			return nil, fmt.Errorf("%w: %w", errCorruptIndex, err)
		}
		size, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errCorruptIndex, err)
		}
		// MaxUint32 alone is not a bound: a bucket claiming four gigabytes for
		// one token is an out-of-memory kill rather than a corrupt-index
		// error, and the caller can recover from the latter. Postings live in
		// this same file, so the file's own size is the real ceiling.
		if size > math.MaxUint32 || size > fileSize {
			return nil, fmt.Errorf("%w: posting block of %d bytes in a %d byte bucket", errCorruptIndex, size, fileSize)
		}
		dirLen += uint64(uvarintLen(ln)) + ln + uint64(uvarintLen(size)) //nolint:gosec // lengths are non-negative
		entries = append(entries, bucketEntry{tok: string(tb), n: uint32(size)})
	}
	pos := dirLen
	for i := range entries {
		entries[i].off = pos
		pos += uint64(entries[i].n)
	}
	return entries, nil
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
		b = binary.AppendUvarint(b, uint64(p.Off-prev)) //nolint:gosec // postings ascend by offset
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
		d := int32(p.Sid - prevSid)       //nolint:gosec // zigzag: the delta's bits read as signed
		z := uint32((d << 1) ^ (d >> 31)) //nolint:gosec // zigzag reinterprets the bits
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

// decodePostingBlock decodes a block read from a bucket and checks the one
// thing every block encodePostings writes holds: offsets that only rise, one
// posting per record. A block whose bytes were zeroed decodes to offset 0 over
// and over, which resolved to some other session's first record, was dropped
// by the session check and left the search answering nothing without an error.
func decodePostingBlock(b []byte) ([]posting, error) {
	posts := decodePostings(b)
	for i := 1; i < len(posts); i++ {
		if posts[i].Off <= posts[i-1].Off {
			return nil, fmt.Errorf("%w: a posting block repeats an offset", errCorruptIndex)
		}
	}
	return posts, nil
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
	// Sized up front: a posting is two varints, and a varint ends on the one
	// byte of it below 0x80. Grown by append, the slice cost twice its size.
	ends := 0
	for _, c := range b {
		if c < 0x80 {
			ends++
		}
	}
	out := make([]posting, 0, ends/2)
	var prev int64
	var prevSid uint32
	for len(b) > 0 {
		d, n := binary.Uvarint(b)
		if n <= 0 {
			return out
		}
		prev += int64(d) //nolint:gosec // deltas of ascending offsets
		b = b[n:]
		v, n := binary.Uvarint(b)
		if n <= 0 {
			return out
		}
		z := uint32(v >> 1) //nolint:gosec // zigzag reinterprets the bits
		sidDelta := int32(z>>1) ^ -int32(z&1)
		sid := prevSid + uint32(sidDelta) //nolint:gosec // zigzag reinterprets the bits
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
	f, err := openIndexFileWrite(p, os.O_RDWR|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return gob.NewEncoder(f).Encode(v)
}

// writeGobAtomic writes to a sibling temp file and renames it over p, so a
// crash mid-write can never leave p half-decoded.
func writeGobAtomic(p string, v any) error {
	_, err := writeGobAtomicStat(p, v)
	return err
}

// writeGobAtomicStat is writeGobAtomic that also returns the file it wrote as
// stat saw it before the rename, which moves the name and not the file.
func writeGobAtomicStat(p string, v any) (os.FileInfo, error) {
	tmp := p + ".tmp"
	f, err := openIndexFileWrite(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return nil, err
	}
	if err = gob.NewEncoder(f).Encode(v); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return nil, err
	}
	// fsync before rename: manifest.gob is the freshness/RecordsSize authority and
	// must land durably last, like records.bin and the buckets. Skipping it left a
	// window where a crash after rename but before flush yields a torn manifest.
	if err = f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return nil, err
	}
	if err = f.Close(); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	// The lock keeps other writers out but not readers, and on Windows a
	// reader decoding manifest.gob refuses the rename onto it with "Access is
	// denied": a recall beside a remember failed that way on windows CI. The
	// reader is a decode away from closing, so wait it out as the swap does.
	if err = renameWaiting(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	// The writer holds the lock, so what is at p is what it wrote.
	fi, err := os.Stat(p)
	if err != nil {
		// Written; only the stat failed, and nil caches nothing.
		return nil, nil //nolint:nilerr,nilnil // written; nil caches nothing
	}
	// Windows reads a file's identity from its path when first compared, and
	// this path moves with the staging directory: read it while it is here.
	_ = os.SameFile(fi, fi)
	return fi, nil
}

func readGob(p string, v any) error {
	f, err := openIndexFile(p)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := gob.NewDecoder(f).Decode(v); err != nil { //nolint:gosec,nolintlint // decodes the index's own files; G709's taint pass fires on some runs only
		return fmt.Errorf("read %s: %w", filepath.Base(p), err)
	}
	return nil
}

// bucketReader is one query's reads of the postings. The relevance tier asks
// for every inflected form of a term, and those forms share their opening
// runes, so they all hash to the same bucket: each bucket's directory is
// looked up once per query, and a form it does not list costs nothing more.
// The directories themselves come from the process's cache (bucketDirOf). A
// file is opened per read, never held, so a concurrent index swap is not
// blocked by a handle.
type bucketReader struct {
	dir string
	// dirs is each file's directory as first looked up, nil for a file that
	// was not there.
	dirs  map[string]*cachedBucketDir
	files map[string]*os.File
	// layers is each bucket's overlays as first looked up.
	layers map[string][]string
	posts  map[string][]posting
	dead   []int64
}

func newBucketReader(dir string) *bucketReader {
	return &bucketReader{
		dir:    dir,
		dirs:   map[string]*cachedBucketDir{},
		files:  map[string]*os.File{},
		layers: map[string][]string{},
		posts:  map[string][]posting{},
		dead:   deadRecordsOf(dir),
	}
}

// bucketBaseRead, when set, runs between a bucket's own postings and its
// overlays'. Tests land a pass there.
var bucketBaseRead func(tok string)

func (b *bucketReader) postings(tok string) ([]posting, error) {
	if bucketBaseRead == nil {
		if cached, ok := b.posts[tok]; ok {
			return cached, nil
		}
	}
	p, ok := bucketPath(b.dir, tok)
	if !ok {
		return nil, nil
	}
	layers, cached := b.layers[p]
	if !cached {
		layers = bucketLayers(p)
		b.layers[p] = layers
	}
	var out []posting
	for i, f := range append([]string{p}, layers...) {
		posts, err := b.filePostings(f, tok)
		if err != nil {
			// A bucket that was never written means the token does not occur
			// — "no postings", not a failure. An overlay merged away since it
			// was listed is in the file it was merged into.
			if os.IsNotExist(err) {
				if i == 0 {
					if bucketBaseRead == nil {
						b.posts[tok] = nil
					}
					return nil, nil
				}
				continue
			}
			return nil, err
		}
		out = joinPostings(out, posts)
		if i == 0 && bucketBaseRead != nil {
			bucketBaseRead(tok)
		}
	}
	res := liveOnly(out, b.dead)
	if bucketBaseRead == nil {
		b.posts[tok] = res
	}
	return res, nil
}

func (b *bucketReader) filePostings(p, tok string) ([]posting, error) {
	d, seen := b.dirs[p]
	var f *os.File
	switch {
	case !seen:
		var err error
		if f, err = openIndexFile(p); err != nil {
			if os.IsNotExist(err) {
				b.dirs[p] = nil
			}
			return nil, err
		}
		if bucketBaseRead != nil || runtime.GOOS == "windows" {
			defer func() { _ = f.Close() }()
		}
		if d, err = bucketDirOf(p, f); err != nil {
			if bucketBaseRead == nil && runtime.GOOS != "windows" {
				_ = f.Close()
			}
			return nil, err
		}
		if bucketBaseRead == nil && runtime.GOOS != "windows" {
			b.files[p] = f
		}
		b.dirs[p] = d
	case d == nil:
		return nil, os.ErrNotExist
	case bucketBaseRead == nil && runtime.GOOS != "windows":
		f = b.files[p]
	}
	if _, ok := d.find(tok); !ok {
		return nil, nil
	}
	if f == nil {
		var err error
		if f, err = openIndexFile(p); err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		// The directory for the file open now, which a swap since the first
		// look may have replaced.
		if d, err = bucketDirOf(p, f); err != nil {
			return nil, err
		}
		b.dirs[p] = d
	}
	e, ok := d.find(tok)
	if !ok {
		return nil, nil
	}
	buf := make([]byte, e.n)
	if _, err := f.ReadAt(buf, int64(e.off)); err != nil { //nolint:gosec // offsets are checked against the bucket size on open
		return nil, err
	}
	return decodePostingBlock(buf)
}

func (b *bucketReader) close() {
	for _, f := range b.files {
		_ = f.Close()
	}
	b.files = nil
	b.dirs = nil
	b.posts = nil
}

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
