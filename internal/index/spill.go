package index

import (
	"bufio"
	"encoding/binary"
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
	workers := runtime.NumCPU()
	if workers < 1 {
		workers = 1
	}
	const batchSize = 512
	jobs := make(chan []tokenJob, workers*4)
	errCh := make(chan error, 1)
	fail := func(err error) {
		select {
		case errCh <- err:
		default:
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bufs := make([][]byte, spillShards)
			seen := map[string]bool{}
			var scratch [binary.MaxVarintLen64]byte
			for batch := range jobs {
				for _, job := range batch {
					eachIndexKey(job.text, job.when, func(tok string) {
						b := bucket(tok)
						seen[b] = true
						sh := shardFor(b)
						buf := bufs[sh]
						n := binary.PutUvarint(scratch[:], uint64(len(tok)))
						buf = append(buf, scratch[:n]...)
						buf = append(buf, tok...)
						n = binary.PutUvarint(scratch[:], uint64(job.offset))
						buf = append(buf, scratch[:n]...)
						v := uint64(job.sid) << 1
						if job.tool {
							v |= 1
						}
						n = binary.PutUvarint(scratch[:], v)
						buf = append(buf, scratch[:n]...)
						bufs[sh] = buf
					})
				}
				for sh, buf := range bufs {
					if len(buf) == 0 {
						continue
					}
					if err := s.append(sh, buf); err != nil {
						fail(err)
					}
					bufs[sh] = buf[:0]
				}
			}
			s.noteBuckets(seen)
		}()
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
	for i := 0; i < spillShards; i++ {
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
	workers := runtime.NumCPU()
	if workers > spillWriters {
		workers = spillWriters
	}
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan int)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for sh := range jobs {
				if err := s.writeShard(dir, sh); err != nil {
					select {
					case errCh <- err:
					default:
					}
				}
			}
		}()
	}
	for sh := 0; sh < spillShards; sh++ {
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

// readSpill turns one spill file back into token -> postings. The postings come
// out in the order they were spilled; writeBucket sorts them anyway.
func readSpill(f *os.File) (map[string][]posting, error) {
	r := bufio.NewReaderSize(f, 1<<16)
	lists := map[string][]posting{}
	var tok []byte
	for {
		n, err := binary.ReadUvarint(r)
		if err == io.EOF {
			return lists, nil
		}
		if err != nil {
			return nil, err
		}
		if uint64(cap(tok)) < n {
			tok = make([]byte, n)
		}
		tok = tok[:n]
		if _, err := io.ReadFull(r, tok); err != nil {
			return nil, err
		}
		off, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, err
		}
		v, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, err
		}
		p := posting{Off: int64(off), Sid: uint32(v >> 1), Tool: v&1 == 1}
		// mapassign_faststr only copies the key when it is new, so a repeated
		// token costs no allocation here.
		lists[string(tok)] = append(lists[string(tok)], p)
	}
}

// cleanup removes the spill files. It has to run before the tmp directory is
// swapped into place — anything left behind ships inside the index — and on
// every error path too. Calling it twice is fine.
func (s *spiller) cleanup() {
	_ = s.flushWriters()
	_ = os.RemoveAll(s.dir)
}
