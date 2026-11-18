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
	writeAll := func(moved bucketPostings) (map[string]bool, error) {
		mergePostings(moved, rp.buckets)
		if err := writeBucketsConcurrent(dst, moved); err != nil {
			return nil, err
		}
		if !rp.m.SearchOnly {
			return cooccurUnindexed(rp.dir, moved), nil
		}
		return nil, nil
	}
	if len(rp.carried.old) == 0 {
		return writeAll(bucketPostings{})
	}
	rp.carried.index()
	src := filepath.Join(rp.dir, "buckets")
	names, readErr := postingBucketNames(src, rp.old.BucketFiles)
	if readErr == nil {
		// Only neighbour-map words need tracking. Each bucket's worker owns
		// its part, so words in other buckets cannot be marked absent here.
		var missing map[string]map[string]bool
		if !rp.m.SearchOnly {
			if m := readCooccur(rp.dir); m != nil {
				missing = map[string]map[string]bool{}
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
			}
		}
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
		var writeErr error
		var wg sync.WaitGroup
		for range min(runtime.NumCPU(), compactPostingsWorkers, len(names)) {
			wg.Go(func() {
				held := false
				defer func() {
					if held {
						compactBucketsHeld.Add(-1)
					}
				}()
				for {
					if held {
						compactBucketsHeld.Add(-1)
						held = false
					}
					i := int(next.Add(1)) - 1
					mu.Lock()
					stopped := readErr != nil || writeErr != nil
					mu.Unlock()
					if i >= len(names) || stopped {
						return
					}
					b := names[i]
					data := map[string][]posting{}
					if old[b] {
						read, err := readBucket(filepath.Join(src, b+".bin"))
						if err != nil {
							mu.Lock()
							if readErr == nil {
								readErr = err
							}
							mu.Unlock()
							return
						}
						held = true
						n := compactBucketsHeld.Add(1)
						for peak := compactBucketsPeak.Load(); n > peak; peak = compactBucketsPeak.Load() {
							if compactBucketsPeak.CompareAndSwap(peak, n) {
								break
							}
						}
						data = rp.carried.move(read)
					}
					fresh, present := rp.buckets[b]
					if len(data) == 0 && !present {
						continue
					}
					for tok, posts := range fresh {
						data[tok] = append(data[tok], posts...)
					}
					if err := writeBucket(filepath.Join(dst, b+".bin"), data); err != nil {
						mu.Lock()
						if writeErr == nil {
							writeErr = err
						}
						mu.Unlock()
						return
					}
					written.Add(1)
					for w := range missing[b] {
						if _, ok := data["t"+w]; ok {
							missing[b][w] = false
						}
					}
				}
			})
		}
		wg.Wait()
		if readErr == nil {
			if writeErr != nil {
				return nil, writeErr
			}
			// A failed read falls back below; count only the writes that stay.
			for range written.Load() {
				reportAdvance(1)
			}
			var out map[string]bool
			if missing != nil {
				out = map[string]bool{}
				for _, words := range missing {
					for w, absent := range words {
						if absent {
							out[w] = true
						}
					}
				}
			}
			return out, nil
		}
		// Retokenizing may not reproduce a damaged bucket's tokens. Leave
		// none of the partial output for the fallback to inherit.
		for _, b := range names {
			for _, suffix := range []string{".bin", ".bin.tmp"} {
				if err := os.Remove(filepath.Join(dst, b+suffix)); err != nil && !os.IsNotExist(err) {
					return nil, err
				}
			}
		}
	}
	// Only a short or unreadable postings directory reaches this fallback,
	// which still builds the whole map.
	moved, err := retokenizeCarried(filepath.Join(rp.dir, "records.bin"), tablesFromManifest(rp.old), rp.carried)
	if err != nil {
		return nil, err
	}
	return writeAll(moved)
}
