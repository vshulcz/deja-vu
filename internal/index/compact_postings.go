package index

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

const compactPostingsWorkers = 8

// compactBucketsHeld and compactBucketsPeak count decoded buckets held by
// streaming workers, so tests can bound the work through the replacing pass.
var compactBucketsHeld, compactBucketsPeak atomic.Int64

// writeCompactedPostings moves and writes a bucket before reading the next.
// Holding all the decoded buckets made compaction grow by gigabytes (#6072).
func (rp *replacePass) writeCompactedPostings() (map[string]bool, error) {
	dst := filepath.Join(rp.tmp, "buckets")
	if len(rp.carried.old) == 0 {
		return rp.writeWholePostings(dst, bucketPostings{})
	}
	rp.carried.index()
	src := filepath.Join(rp.dir, "buckets")
	if names, err := postingBucketNames(src, rp.old.BucketFiles); err == nil {
		unposted, readFailed, streamErr := rp.streamCompactedBuckets(src, dst, names)
		if !readFailed {
			return unposted, streamErr
		}
		if streamErr != nil {
			return nil, streamErr
		}
	}
	// Only a short or unreadable postings directory reaches this fallback,
	// which still builds the whole map.
	moved, err := retokenizeCarried(filepath.Join(rp.dir, "records.bin"), tablesFromManifest(rp.old), rp.carried)
	if err != nil {
		return nil, err
	}
	return rp.writeWholePostings(dst, moved)
}

func (rp *replacePass) writeWholePostings(dst string, moved bucketPostings) (map[string]bool, error) {
	mergePostings(moved, rp.buckets)
	if err := writeBucketsConcurrent(dst, moved); err != nil {
		return nil, err
	}
	var unposted map[string]bool
	if !rp.m.SearchOnly {
		unposted = cooccurUnindexed(rp.dir, moved)
	}
	return unposted, nil
}

// cooccurBuckets groups neighbour-map words by their bucket so each bucket
// worker can record absent words concurrently without cross-bucket locking.
func cooccurBuckets(dir string, searchOnly bool) map[string]map[string]bool {
	if searchOnly {
		return nil
	}
	m := readCooccur(dir)
	if m == nil {
		return nil
	}
	missing := map[string]map[string]bool{}
	add := func(w string) {
		b := bucket("t" + w)
		if missing[b] == nil {
			missing[b] = map[string]bool{}
		}
		missing[b][w] = true
	}
	for w, ns := range m {
		add(w)
		for _, n := range ns {
			add(n)
		}
	}
	return missing
}

func collectAbsentWords(missing map[string]map[string]bool) map[string]bool {
	if missing == nil {
		return nil
	}
	out := map[string]bool{}
	for _, words := range missing {
		for w, absent := range words {
			if absent {
				out[w] = true
			}
		}
	}
	return out
}

func removePartialBuckets(dst string, names []string) error {
	for _, b := range names {
		for _, suffix := range []string{".bin", ".bin.tmp"} {
			if err := os.Remove(filepath.Join(dst, b+suffix)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func recordCompactBucketPeak(n int64) {
	for peak := compactBucketsPeak.Load(); n > peak; peak = compactBucketsPeak.Load() {
		if compactBucketsPeak.CompareAndSwap(peak, n) {
			break
		}
	}
}

func (rp *replacePass) compactBucket(src, dst, b string, isOld bool, missingWords map[string]bool) (bool, error, error) {
	data := map[string][]posting{}
	if isOld {
		read, err := readBucket(filepath.Join(src, b+".bin"))
		if err != nil {
			return false, err, nil
		}
		n := compactBucketsHeld.Add(1)
		defer compactBucketsHeld.Add(-1)
		recordCompactBucketPeak(n)
		data = rp.carried.move(read)
	}
	fresh, present := rp.buckets[b]
	if len(data) == 0 && !present {
		return false, nil, nil
	}
	for tok, posts := range fresh {
		data[tok] = append(data[tok], posts...)
	}
	if err := writeBucket(filepath.Join(dst, b+".bin"), data); err != nil {
		return false, nil, err
	}
	for w := range missingWords {
		if _, ok := data["t"+w]; ok {
			missingWords[w] = false
		}
	}
	return true, nil, nil
}

func (rp *replacePass) streamCompactedBuckets(src, dst string, names []string) (map[string]bool, bool, error) {
	missing := cooccurBuckets(rp.dir, rp.m.SearchOnly)
	old := make(map[string]bool, len(names))
	for i, n := range names {
		names[i] = strings.TrimSuffix(n, ".bin")
		old[names[i]] = true
	}
	for b := range rp.buckets {
		if !old[b] {
			names = append(names, b)
		}
	}

	var next, written atomic.Int64
	var mu sync.Mutex
	var readErr, writeErr error
	stopped := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return readErr != nil || writeErr != nil
	}
	var wg sync.WaitGroup
	for range min(runtime.NumCPU(), compactPostingsWorkers, len(names)) {
		wg.Go(func() {
			for !stopped() {
				i := int(next.Add(1)) - 1
				if i >= len(names) {
					return
				}
				b := names[i]
				wrote, rerr, werr := rp.compactBucket(src, dst, b, old[b], missing[b])
				if rerr != nil {
					mu.Lock()
					if readErr == nil {
						readErr = rerr
					}
					mu.Unlock()
					return
				}
				if werr != nil {
					mu.Lock()
					if writeErr == nil {
						writeErr = werr
					}
					mu.Unlock()
					return
				}
				if wrote {
					written.Add(1)
				}
			}
		})
	}
	wg.Wait()

	if readErr != nil {
		// Retokenizing may not reproduce a damaged bucket's tokens. Leave
		// none of the partial output for the fallback to inherit.
		return nil, true, removePartialBuckets(dst, names)
	}
	if writeErr != nil {
		return nil, false, writeErr
	}
	// A failed read falls back to retokenize; count only the writes that stay.
	for range written.Load() {
		reportAdvance(1)
	}
	return collectAbsentWords(missing), false, nil
}
