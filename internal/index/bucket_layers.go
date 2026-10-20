package index

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// A pass that adds a few sessions used to write every postings file again: a
// bucket holds every token that opens with the same two letters, so the few
// thousand tokens of one session reach nearly all of them, and on a
// 25,000-session store that was 208 MB a pass whatever it added (#5387).
//
// Now the postings a pass adds go into an overlay beside the bucket, tx.1.ov
// next to tx.bin, in the bucket format. A reader joins the bucket and its
// overlays and drops the postings of dead records. Overlays are merged pairwise
// while the newer is at least half the size of the one before it, so a posting
// is copied a few times rather than once a pass, and into the bucket once they
// hold a quarter of it.

// maxOverlays caps the files a read of one bucket opens.
const maxOverlays = 8

// overlayPath is the n-th overlay of the bucket at base, counting from 1.
func overlayPath(base string, n int) string {
	return strings.TrimSuffix(base, ".bin") + "." + strconv.Itoa(n) + ".ov"
}

// isOverlay reports whether a file in buckets/ is an overlay.
func isOverlay(name string) bool { return strings.HasSuffix(name, ".ov") }

// isPostingsFile reports whether a file in buckets/ is a bucket or an overlay.
func isPostingsFile(name string) bool { return strings.HasSuffix(name, ".bin") || isOverlay(name) }

// bucketLayers is the overlays of the bucket at base, oldest first. They are
// numbered without gaps; a merge removes the newest first, so a reader that
// stops at the first missing one never skips a file.
func bucketLayers(base string) []string {
	var out []string
	for n := 1; ; n++ {
		p := overlayPath(base, n)
		if _, err := os.Lstat(p); err != nil {
			return out
		}
		out = append(out, p)
	}
}

// joinPostings appends b, read from a later file, to a. Each is sorted by
// offset and a later file's postings point past an earlier one's, so this is
// an append, except while a merge in place has the same posting in two files.
func joinPostings(a, b []posting) []posting {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	if a[len(a)-1].Off < b[0].Off {
		return append(a, b...)
	}
	return sortedUniquePostings(append(a, b...))
}

// liveOnly drops the postings of dead records. Both are sorted by offset.
func liveOnly(posts []posting, dead []int64) []posting {
	if len(dead) == 0 || len(posts) == 0 {
		return posts
	}
	out := posts[:0:0]
	for _, p := range posts {
		i, ok := slices.BinarySearch(dead, p.Off)
		if ok {
			continue
		}
		dead = dead[i:]
		out = append(out, p)
	}
	return out
}

// deadRecordsOf is the dead records of the index in dir, nil when it cannot
// say. A postings read and the manifest are separate reads, as they always
// were; a swap between them is the window a reader already rides out.
func deadRecordsOf(dir string) []int64 {
	m, err := readManifestCached(dir)
	if err != nil {
		return nil
	}
	return m.DeadRecords
}

// readBucket reads a bucket with its overlays joined in. Dead records are not
// dropped here: the callers that write buckets again drop them themselves.
func readBucket(p string) (map[string][]posting, error) {
	out, err := readBucketFile(p)
	if err != nil {
		return nil, err
	}
	for _, ov := range bucketLayers(p) {
		add, err := readBucketFile(ov)
		if err != nil {
			if os.IsNotExist(err) {
				break
			}
			return nil, err
		}
		for tok, posts := range add {
			out[tok] = joinPostings(out[tok], posts)
		}
	}
	return out, nil
}

// readBucketToken is one token's live postings from the bucket at p and its
// overlays.
func readBucketToken(p, tok string, dead []int64) ([]posting, error) {
	var out []posting
	for i, f := range append([]string{p}, bucketLayers(p)...) {
		posts, err := readBucketFileToken(f, tok)
		if err != nil {
			// A bucket that was never written means the token simply does not
			// occur — that is "no postings", not a failure. Erroring here made
			// the stem tier abort whole searches over an absent shard.
			if os.IsNotExist(err) {
				if i == 0 {
					return nil, nil
				}
				break
			}
			return nil, err
		}
		out = joinPostings(out, posts)
	}
	return liveOnly(out, dead), nil
}

// addOverlays files adds, a pass's new postings by bucket, beside the buckets
// of src into dst, and merges what has grown enough to merge. dead is the
// index's dead records after the pass; a merge leaves their postings out.
//
// With dst a new directory, every file of src the pass keeps is hard-linked
// in and every file it changes is written new beside them, so nothing in dst
// is written or removed through a link: on Windows a reader holding the live
// file open would refuse that.
//
// With dst the same as src the work happens in place. Each step renames a
// whole file in and then removes the files it repeats, newest first, so a
// reader sees the old layers, or the new with a repeat it joins away. A
// reader that listed the layers before a merge can miss the merged ones for
// that one read.
func addOverlays(src, dst string, adds bucketPostings, dead []int64) error {
	if src != dst {
		if err := linkBuckets(src, dst, adds); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(adds))
	for b := range adds {
		names = append(names, b)
	}
	slices.Sort(names)
	workers := min(runtime.NumCPU(), 8, len(names))
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		failed error
		next   int
	)
	take := func() (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		if next >= len(names) || failed != nil {
			return "", false
		}
		next++
		return names[next-1], true
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				b, ok := take()
				if !ok {
					return
				}
				reportAdvance(1)
				if err := addOverlay(filepath.Join(src, b+".bin"), filepath.Join(dst, b+".bin"), adds[b], dead); err != nil {
					mu.Lock()
					if failed == nil {
						failed = err
					}
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	return failed
}

// addOverlay files add for the bucket at src into dst. The plan is made from
// the sizes in src: the new overlay folds into the one before it while that
// one is no more than twice its size, and everything folds into the bucket
// once the overlays hold a quarter of it.
func addOverlay(src, dst string, add map[string][]posting, dead []int64) error {
	inPlace := src == dst
	bi, err := os.Stat(src)
	if os.IsNotExist(err) {
		return writeBucket(dst, add)
	}
	if err != nil {
		return err
	}
	layers := bucketLayers(src)
	sizes := make([]int64, 0, len(layers)+1)
	for _, p := range layers {
		fi, err := os.Stat(p)
		if err != nil {
			return err
		}
		sizes = append(sizes, fi.Size())
	}
	sizes = append(sizes, int64(len(encodeBucketEstimate(add))))
	// from is the first layer the new file takes in, counting from 1.
	from := len(sizes)
	for from >= 2 && sizes[from-2] <= 2*sizes[from-1] {
		sizes[from-2] += sizes[from-1]
		sizes = sizes[:from-1]
		from--
	}
	var held int64
	for _, n := range sizes {
		held += n
	}
	if len(sizes) > maxOverlays || held*4 > bi.Size() {
		data, err := readBucket(src)
		if err != nil {
			return err
		}
		for tok, posts := range add {
			data[tok] = joinPostings(data[tok], posts)
		}
		if err := writeBucket(dst, liveBucket(data, dead)); err != nil {
			return err
		}
		if inPlace {
			dropLayers(dst, 1, len(layers))
		}
		return nil
	}
	merged := map[string][]posting{}
	for _, p := range layers[from-1:] {
		data, err := readBucketFile(p)
		if err != nil {
			return err
		}
		for tok, posts := range data {
			merged[tok] = joinPostings(merged[tok], posts)
		}
	}
	for tok, posts := range add {
		merged[tok] = joinPostings(merged[tok], posts)
	}
	if !inPlace {
		if err := os.Link(src, dst); err != nil {
			return err
		}
		for i, p := range layers[:from-1] {
			if err := os.Link(p, overlayPath(dst, i+1)); err != nil {
				return err
			}
		}
	}
	if err := writeBucket(overlayPath(dst, from), liveBucket(merged, dead)); err != nil {
		return err
	}
	if inPlace {
		dropLayers(dst, from+1, len(layers))
	}
	return nil
}

// dropLayers removes the overlays numbered first to last of the bucket at
// base, newest first. A file that will not go (Windows, a reader holding it)
// stops it there: what is left repeats postings the merged file holds, which
// a read joins away and the next merge takes in, and no gap opens below it.
func dropLayers(base string, first, last int) {
	for i := last; i >= first; i-- {
		if err := os.Remove(overlayPath(base, i)); err != nil && !os.IsNotExist(err) {
			return
		}
	}
}

// encodeBucketEstimate is the size an overlay of data comes to, near enough
// to plan merges by: the postings blocks without the directory.
func encodeBucketEstimate(data map[string][]posting) []byte {
	var out []byte
	for tok, posts := range data {
		out = append(out, tok...)
		out = append(out, encodePostings(posts)...)
	}
	return out
}

// liveBucket drops dead records' postings, and the tokens left with none.
func liveBucket(data map[string][]posting, dead []int64) map[string][]posting {
	for tok, posts := range data {
		if live := liveOnly(posts, dead); len(live) > 0 {
			data[tok] = live
		} else {
			delete(data, tok)
		}
	}
	return data
}

// linkBuckets hard-links into to every file of the bucket directory from
// that belongs to a bucket the pass leaves alone, so a pass that stages its
// index in a new directory starts from the postings it has without copying
// them.
func linkBuckets(from, to string, touched bucketPostings) error {
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !isPostingsFile(name) {
			continue
		}
		b, _, _ := strings.Cut(name, ".")
		if _, ok := touched[b]; ok {
			continue
		}
		if err := os.Link(filepath.Join(from, name), filepath.Join(to, name)); err != nil {
			return err
		}
	}
	return nil
}
