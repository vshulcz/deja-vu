package index

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// A replacement pass rewrites records.bin whole, and most of what it writes is
// carried: on a 12,000-session store a pass over 9 changed transcripts wrote
// 257 sessions anew and every other record again. Each carried record used to
// be inflated, deflated and tokenized a second time, which put 74% of the
// pass on one core in work whose answer the old index already held (#5383).
// A carried record is copied as its stored bytes now, under ids from the new
// string table, and its postings are the old ones moved to its new offset.

// carriedRecords maps each carried record's offset in the old log to the one
// it has in the new log, with the session id and tool bit its postings carry.
// Appended in log order, so old is sorted.
type carriedRecords struct {
	old  []int64
	new  []int64
	sid  []uint32
	tool []bool
	// page[k] is the first record at or past offset k<<carryPageShift, so a
	// lookup searches the few records of one page rather than all of them:
	// the whole-slice search was a cache miss a step, 30% of the pass.
	page []int32
}

const carryPageShift = 12

// index builds the page table once every record is added.
func (c *carriedRecords) index() {
	if len(c.old) == 0 {
		return
	}
	pages := int(c.old[len(c.old)-1]>>carryPageShift) + 2
	c.page = make([]int32, pages)
	i := 0
	for k := range c.page {
		for i < len(c.old) && c.old[i] < int64(k)<<carryPageShift {
			i++
		}
		c.page[k] = int32(i)
	}
}

// find is the position of the record at off, or false when none was carried.
func (c *carriedRecords) find(off int64) (int, bool) {
	k := off >> carryPageShift
	if off < 0 || k+1 >= int64(len(c.page)) {
		return 0, false
	}
	lo, hi := int(c.page[k]), int(c.page[k+1])
	i := lo + sort.Search(hi-lo, func(j int) bool { return c.old[lo+j] >= off })
	return i, i < hi && c.old[i] == off
}

func (c *carriedRecords) add(oldOff, newOff int64, sid uint32, tool bool) {
	c.old = append(c.old, oldOff)
	c.new = append(c.new, newOff)
	c.sid = append(c.sid, sid)
	c.tool = append(c.tool, tool)
}

// passKeyed and passEncoded count the records a replacement pass tokenized
// and encoded. A test holds them to what the pass replaced, so the work stays
// bounded by the change rather than by the store.
var passKeyed, passEncoded atomic.Int64

// recordPrefix splits a stored record into its three interned ids and the
// rest: the encoding flag and the body, copied as they are.
func recordPrefix(b []byte) (kid, pid, rid uint64, rest []byte, ok bool) {
	var n int
	if kid, n = binary.Uvarint(b); n <= 0 {
		return 0, 0, 0, nil, false
	}
	b = b[n:]
	if pid, n = binary.Uvarint(b); n <= 0 {
		return 0, 0, 0, nil, false
	}
	b = b[n:]
	if rid, n = binary.Uvarint(b); n <= 0 {
		return 0, 0, 0, nil, false
	}
	b = b[n:]
	if len(b) < 1 {
		return 0, 0, 0, nil, false
	}
	// What decodeRecord would refuse without inflating anything: an encoding
	// it does not know, or a plain body shorter than its own fields.
	switch b[0] {
	case recordDeflated:
	case recordRaw:
		if len(b) < 9 {
			return 0, 0, 0, nil, false
		}
		if _, _, ok := consumeField(b[9:]); !ok {
			return 0, 0, 0, nil, false
		}
	default:
		return 0, 0, 0, nil, false
	}
	return kid, pid, rid, b, true
}

// writeStored writes a record from its stored body, re-interning only the
// three ids, which the new table numbers in its own order.
func (rw *recordWriter) writeStored(key, path, role string, rest []byte) (int64, error) {
	var pre [3 * binary.MaxVarintLen64]byte
	p := binary.AppendUvarint(pre[:0], rw.tables.intern(key))
	p = binary.AppendUvarint(p, rw.tables.intern(path))
	p = binary.AppendUvarint(p, rw.tables.intern(role))
	size := len(p) + len(rest)
	if size > 1<<31 {
		return 0, fmt.Errorf("record too large")
	}
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(size))
	if _, err := rw.w.Write(hdr[:]); err != nil {
		return 0, err
	}
	if _, err := rw.w.Write(p); err != nil {
		return 0, err
	}
	if _, err := rw.w.Write(rest); err != nil {
		return 0, err
	}
	off := rw.off
	rw.off += int64(len(hdr)) + int64(size)
	return off, nil
}

// eachStoredRecord walks the live records of the log handing fn each one's
// offset and payload, undecoded. The payload is reused for the next record. A
// torn tail ends the walk the way it ends eachRecord.
func eachStoredRecord(path string, t *recordTables, fn func(off int64, payload []byte) error) error {
	f, err := openIndexFile(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w := newLogWalk(f, t, 0)
	for {
		off, payload, err := w.next()
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			}
			return err
		}
		if err := fn(off, payload); err != nil {
			return err
		}
	}
}

// carriedPostings is the postings of the carried records: the old index's,
// each moved to the record's new offset. A posting whose record was not
// carried goes with it. The old buckets are read in parallel, one file to a
// worker, since a bucket's tokens are its own.
//
// It answers what tokenizing the records again would, because a store of
// another tokenizer is a store of another version and never reaches this pass.
// A postings directory that is not whole cannot be trusted to; the records are
// tokenized again from the old log then, the way they always were.
func carriedPostings(dir string, old Manifest, c *carriedRecords) (bucketPostings, error) {
	if len(c.old) == 0 {
		return bucketPostings{}, nil
	}
	c.index()
	if out, err := movedPostings(filepath.Join(dir, "buckets"), old.BucketFiles, c); err == nil {
		return out, nil
	}
	return retokenizeCarried(filepath.Join(dir, "records.bin"), tablesFromManifest(old), c)
}

var errBucketsShort = errors.New("postings files missing")

func movedPostings(bdir string, want int, c *carriedRecords) (bucketPostings, error) {
	entries, err := os.ReadDir(bdir)
	if err != nil {
		return nil, err
	}
	// Read by bucket, each with its overlays joined in; counted with them, as
	// the manifest counts (#5426).
	var names []string
	files := 0
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !isPostingsFile(n) {
			continue
		}
		files++
		if strings.HasSuffix(n, ".bin") {
			names = append(names, n)
		}
	}
	if len(names) == 0 || files < want {
		return nil, errBucketsShort
	}
	workers := min(runtime.NumCPU(), len(names))
	parts := make([]map[string][]posting, len(names))
	var next atomic.Int64
	// The first error, under a lock: readBucket's errors are of several
	// types, which an atomic.Value refuses to hold in turn.
	var mu sync.Mutex
	var failed error
	fail := func(err error) {
		mu.Lock()
		if failed == nil {
			failed = err
		}
		mu.Unlock()
	}
	stopped := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return failed != nil
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(names) || stopped() {
					return
				}
				data, err := readBucket(filepath.Join(bdir, names[i]))
				if err != nil {
					fail(err)
					return
				}
				parts[i] = c.move(data)
			}
		}()
	}
	wg.Wait()
	if failed != nil {
		return nil, failed
	}
	out := make(bucketPostings, len(names))
	for i, n := range names {
		if len(parts[i]) > 0 {
			out[strings.TrimSuffix(n, ".bin")] = parts[i]
		}
	}
	return out, nil
}

// move rewrites one bucket's postings onto the new log, dropping those of
// records that were not carried.
func (c *carriedRecords) move(data map[string][]posting) map[string][]posting {
	out := make(map[string][]posting, len(data))
	for tok, posts := range data {
		// Moved in place: the block is this bucket's own copy, and a posting
		// never moves to a slot ahead of the one it is read from.
		moved := posts[:0]
		for _, p := range posts {
			if i, ok := c.find(p.Off); ok {
				moved = append(moved, posting{Off: c.new[i], Sid: c.sid[i], Tool: c.tool[i]})
			}
		}
		if len(moved) > 0 {
			out[tok] = moved
		}
	}
	return out
}

// retokenizeCarried keys the carried records from their text, in parallel,
// for an old index whose postings cannot be moved.
func retokenizeCarried(path string, t *recordTables, c *carriedRecords) (bucketPostings, error) {
	f, err := openIndexFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return indexTextParallel(func(push func(tokenJob)) error {
		for i, off := range c.old {
			r, err := readRecordAt(f, off, t)
			if err != nil {
				return err
			}
			passKeyed.Add(1)
			push(tokenJob{text: tokenizedPart(r.Role, r.Text), offset: c.new[i], sid: c.sid[i], when: r.Time, tool: c.tool[i]})
		}
		return nil
	})
}

// mergePostings adds from into into.
func mergePostings(into, from bucketPostings) {
	for b, toks := range from {
		if into[b] == nil {
			into[b] = toks
			continue
		}
		for tok, posts := range toks {
			into[b][tok] = append(into[b][tok], posts...)
		}
	}
}
