package index

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// A full build used to assemble the whole token -> postings map in RAM and only
// write buckets/ once the last message was read: about sixteen times the store
// in live heap, held all at once, which a 500 MB store turns into something no
// laptop has (#1084).
//
// The build is two passes now. Pass one writes every (token, posting) pair
// straight to a spill file as it is produced; pass two reads one spill file
// back, rebuilds the postings for the buckets it holds, and writes them. Only
// one spill file's worth of postings is ever live, so the ceiling is a shard
// rather than the index. The bucket files themselves are byte for byte what
// they were: writeBucket sorts and dedupes postings, so the order they were
// appended in never reached the disk.
const spillShards = 128

// spillWriters is how many spill files pass two holds in memory at once, and so
// the multiple on the ceiling. A bucket is a big unit — tokens are prefixed, so
// an ASCII corpus lands in about 27 of them — and two keeps the write phase
// parallel without letting half the index be live again.
const spillWriters = 2

// spiller fans postings out to spillShards files keyed by the bucket they will
// end up in, so a shard can be assembled and written without touching any
// other.
type spiller struct {
	dir string
	mu  [spillShards]sync.Mutex
	f   [spillShards]*os.File
	w   [spillShards]*bufio.Writer

	bmu     sync.Mutex
	buckets map[string]bool
}

func newSpiller(tmp string) (*spiller, error) {
	d := filepath.Join(tmp, "spill")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return nil, err
	}
	return &spiller{dir: d, buckets: map[string]bool{}}, nil
}

func shardName(i int) string { return fmt.Sprintf("%03d.spill", i) }

// shardFor keeps every token of a bucket in one spill file: pass two writes a
// bucket from a single file or it would have to hold them all open.
func shardFor(b string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(b))
	return int(h.Sum32() % spillShards)
}

// append writes one worker's batch for a shard. Workers buffer locally and take
// the lock once per shard per batch rather than once per token.
func (s *spiller) append(i int, b []byte) error {
	s.mu[i].Lock()
	defer s.mu[i].Unlock()
	if s.w[i] == nil {
		f, err := os.OpenFile(filepath.Join(s.dir, shardName(i)), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		s.f[i] = f
		s.w[i] = bufio.NewWriterSize(f, 1<<16)
	}
	_, err := s.w[i].Write(b)
	return err
}

func (s *spiller) noteBuckets(seen map[string]bool) {
	s.bmu.Lock()
	defer s.bmu.Unlock()
	for b := range seen {
		s.buckets[b] = true
	}
}

// bucketCount is how many bucket files pass two will write, for the progress
// bar that used to read it off the in-memory map.
func (s *spiller) bucketCount() int {
	s.bmu.Lock()
	defer s.bmu.Unlock()
	return len(s.buckets)
}

// run is pass one: it hands the feed a push callback and moves jobs to the
// workers in batches: one channel send per message caused enough scheduler
// wakeups to show up as ~20% of a cold rebuild profile.
func (s *spiller) run(feed func(push func(tokenJob)) error) error {
	workers := max(runtime.NumCPU(), 1)
	// A batch is also the window a worker groups postings by token in: on a
	// 12k-session store 2048 messages spill 0.59 GB where 512 spill 0.77 GB
	// (#5526).
	const batchSize = 2048
	jobs := make(chan []tokenJob, workers*4)
	errCh := make(chan error, 1)
	fail := func(err error) {
		select {
		case errCh <- err:
		default:
		}
	}
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			g := newSpillGroups()
			seen := map[string]bool{}
			flush := func() {
				for sh, buf := range g.encode(seen) {
					if len(buf) == 0 {
						continue
					}
					if err := s.append(sh, buf); err != nil {
						fail(err)
					}
				}
			}
			var k keyer
			var job tokenJob
			add := func(tok []byte) { g.add(tok, job.offset, job.sid, job.tool) }
			for batch := range jobs {
				for _, job = range batch {
					k.keys(job.text, job.when, add)
					// A batch of CJK text is mostly distinct bigrams: held
					// whole, it would cost far more than the spill it saves.
					if g.held >= spillGroupBytes {
						flush()
					}
				}
				flush()
				// Work done, not work handed over: the phase that feeds this
				// pool used to report a session the moment it was pushed, so
				// the bar read 99% while the workers still had a third of the
				// corpus to go (#3372).
				reportAdvance(len(batch))
			}
			s.noteBuckets(seen)
		})
	}
	batch := make([]tokenJob, 0, batchSize)
	push := func(j tokenJob) {
		batch = append(batch, j)
		if len(batch) == batchSize {
			jobs <- batch
			batch = make([]tokenJob, 0, batchSize)
		}
	}
	err := feed(push)
	if len(batch) > 0 {
		jobs <- batch
	}
	close(jobs)
	wg.Wait()
	if err != nil {
		return err
	}
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

// flushWriters ends pass one: the buffers have to reach disk before anything
// reads a spill file back.
func (s *spiller) flushWriters() error {
	var first error
	for i := range spillShards {
		s.mu[i].Lock()
		if s.w[i] != nil {
			if err := s.w[i].Flush(); err != nil && first == nil {
				first = err
			}
			s.w[i] = nil
		}
		if s.f[i] != nil {
			if err := s.f[i].Close(); err != nil && first == nil {
				first = err
			}
			s.f[i] = nil
		}
		s.mu[i].Unlock()
	}
	return first
}

// writeBuckets is pass two: each spill file becomes the bucket files it holds,
// and its postings are freed before the next one is read.
func (s *spiller) writeBuckets(dir string) error {
	if err := s.flushWriters(); err != nil {
		return err
	}
	workers := max(min(runtime.NumCPU(), spillWriters), 1)
	jobs := make(chan int)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for sh := range jobs {
				if err := s.writeShard(dir, sh); err != nil {
					select {
					case errCh <- err:
					default:
					}
				}
			}
		})
	}
	for sh := range spillShards {
		jobs <- sh
	}
	close(jobs)
	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func (s *spiller) writeShard(dir string, sh int) error {
	f, err := os.Open(filepath.Join(s.dir, shardName(sh)))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	lists, err := readSpill(f)
	_ = f.Close()
	if err != nil {
		return err
	}
	// Group by bucket only once per distinct token: bucket() walks the runes,
	// and a shard holds far more postings than tokens.
	byBucket := map[string]map[string][]posting{}
	for tok, posts := range lists {
		b := bucket(tok)
		if byBucket[b] == nil {
			byBucket[b] = map[string][]posting{}
		}
		byBucket[b][tok] = posts
		delete(lists, tok)
	}
	for b, data := range byBucket {
		reportAdvance(1)
		if err := writeBucket(filepath.Join(dir, b+".bin"), data); err != nil {
			return err
		}
		delete(byBucket, b)
	}
	return nil
}

// spillGroups is one worker's batch, gathered by token before it is spilled.
// Written a posting at a time, every posting carried its token and a full
// offset and session: 2.4 GB of spill for a 12k-session store, and on a disk
// that took 30-75 MB/s the workers spent the phase in write(2) or queued on
// the shard lock behind one that was (#5526). A token is written once per
// batch now, followed by its postings as differences.
type spillGroups struct {
	idx    map[string]int32
	groups []spillGroup
	bufs   [][]byte
	// held is roughly what the batch costs in memory so far.
	held int
}

// spillGroupBytes caps what one worker gathers before it spills early.
const spillGroupBytes = 16 << 20

type spillGroup struct {
	tok     string
	n       int
	last    uint64
	lastSid uint32
	posts   []byte
}

func newSpillGroups() *spillGroups {
	return &spillGroups{idx: map[string]int32{}, bufs: make([][]byte, spillShards)}
}

// add takes a message's keys repeats and all: a key's postings are gathered
// one message at a time, so a repeat is a posting at the offset it already
// ends on. That is the dedupe eachIndexKey does with a map per message.
//
// The key is the keyer's buffer: it is copied only when the batch first sees
// it, and looking it up converts nothing.
func (g *spillGroups) add(key []byte, off int64, sid uint32, tool bool) {
	i, ok := g.idx[string(key)]
	if ok && g.groups[i].last == uint64(off) { //nolint:gosec // record offsets are non-negative
		return
	}
	if !ok {
		tok := string(key)
		i = int32(len(g.idx)) //nolint:gosec // a batch holds far fewer than 2^31 tokens
		g.idx[tok] = i
		if int(i) == len(g.groups) {
			g.groups = append(g.groups, spillGroup{})
		}
		g.groups[i] = spillGroup{tok: tok, posts: g.groups[i].posts[:0]}
		g.held += len(tok) + 64
	}
	g.held += 8
	gr := &g.groups[i]
	// Both as differences modulo 2^64 and 2^32, so a value that steps back
	// still round-trips. In feed order offsets only rise and the session
	// mostly repeats; the session goes zigzag, as in encodePostings.
	gr.posts = binary.AppendUvarint(gr.posts, uint64(off)-gr.last) //nolint:gosec // record offsets are non-negative
	d := int32(sid - gr.lastSid)                                   //nolint:gosec // zigzag: the delta's bits read as signed
	v := uint64(uint32((d<<1)^(d>>31))) << 1                       //nolint:gosec // zigzag reinterprets the bits
	if tool {
		v |= 1
	}
	gr.posts = binary.AppendUvarint(gr.posts, v)
	gr.last = uint64(off) //nolint:gosec // record offsets are non-negative
	gr.lastSid = sid
	gr.n++
}

// encode turns the batch into one buffer per shard, notes the buckets it
// touched in seen, and empties the batch. The buffers are reused by the next
// call.
func (g *spillGroups) encode(seen map[string]bool) [][]byte {
	for sh := range g.bufs {
		g.bufs[sh] = g.bufs[sh][:0]
	}
	for i := range len(g.idx) {
		gr := &g.groups[i]
		b := bucket(gr.tok)
		seen[b] = true
		sh := shardFor(b)
		buf := binary.AppendUvarint(g.bufs[sh], uint64(len(gr.tok)))
		buf = append(buf, gr.tok...)
		buf = binary.AppendUvarint(buf, uint64(gr.n)) //nolint:gosec // counts are non-negative
		g.bufs[sh] = append(buf, gr.posts...)
		gr.tok = ""
	}
	clear(g.idx)
	g.held = 0
	return g.bufs
}

// readSpill turns one spill file back into token -> postings. The postings come
// out in the order they were spilled; writeBucket sorts them anyway.
func readSpill(f *os.File) (map[string][]posting, error) {
	r := bufio.NewReaderSize(f, 1<<16)
	lists := map[string][]posting{}
	var tok []byte
	for {
		n, err := binary.ReadUvarint(r)
		if errors.Is(err, io.EOF) {
			return lists, nil
		}
		if err != nil {
			return nil, err
		}
		if uint64(cap(tok)) < n {
			tok = make([]byte, n)
		}
		tok = tok[:n]
		if _, err = io.ReadFull(r, tok); err != nil {
			return nil, err
		}
		count, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, err
		}
		// mapassign_faststr only copies the key when it is new, so a repeated
		// token costs no allocation here.
		posts := lists[string(tok)]
		var off uint64
		var sid uint32
		for range count {
			d, err := binary.ReadUvarint(r)
			if err != nil {
				return nil, err
			}
			v, err := binary.ReadUvarint(r)
			if err != nil {
				return nil, err
			}
			off += d
			z := uint32(v >> 1)                                                       //nolint:gosec // zigzag reinterprets the bits
			sid += uint32(int32(z>>1) ^ -int32(z&1))                                  //nolint:gosec // zigzag reinterprets the bits
			posts = append(posts, posting{Off: int64(off), Sid: sid, Tool: v&1 == 1}) //nolint:gosec // written from a non-negative offset
		}
		lists[string(tok)] = posts
	}
}

// cleanup removes the spill files. It has to run before the tmp directory is
// swapped into place — anything left behind ships inside the index — and on
// every error path too. Calling it twice is fine.
func (s *spiller) cleanup() {
	_ = s.flushWriters()
	_ = os.RemoveAll(s.dir)
}
