package index

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
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
// whole file in, and the files it repeats are handed back, each bucket's
// newest first, for the caller to remove once its manifest commits (see
// dropStale): a reader sees the old layers, or the new with a repeat it joins
// away, and the files on disk never fall below the count the manifest holds.
func addOverlays(src, dst string, adds bucketPostings, dead []int64) (stale [][]string, _ error) {
	if src != dst {
		if err := linkBuckets(src, dst, adds); err != nil {
			return nil, err
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
				gone, err := addOverlay(filepath.Join(src, b+".bin"), filepath.Join(dst, b+".bin"), adds[b], dead)
				mu.Lock()
				if err != nil && failed == nil {
					failed = err
				}
				if len(gone) > 0 {
					stale = append(stale, gone)
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return stale, failed
}

// addOverlay files add for the bucket at src into dst. The plan is made from
// the sizes in src: the new overlay folds into the one before it while that
// one is no more than twice its size, and everything folds into the bucket
// once the overlays hold a quarter of it. In place, it returns the overlays
// the step made redundant, newest first.
func addOverlay(src, dst string, add map[string][]posting, dead []int64) ([]string, error) {
	inPlace := src == dst
	bi, err := os.Stat(src)
	if os.IsNotExist(err) {
		if len(add) == 0 {
			return nil, nil
		}
		return nil, writeBucket(dst, add)
	}
	if err != nil {
		return nil, err
	}
	layers := bucketLayers(src)
	sizes := make([]int64, 0, len(layers)+1)
	for _, p := range layers {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, err
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
			return nil, err
		}
		for tok, posts := range add {
			data[tok] = joinPostings(data[tok], posts)
		}
		if err := writeBucket(dst, liveBucket(data, dead)); err != nil {
			return nil, err
		}
		if inPlace {
			return newestFirst(dst, 1, len(layers)), nil
		}
		return nil, nil
	}
	merged := map[string][]posting{}
	for _, p := range layers[from-1:] {
		data, err := readBucketFile(p)
		if err != nil {
			return nil, err
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
			return nil, err
		}
		for i, p := range layers[:from-1] {
			if err := os.Link(p, overlayPath(dst, i+1)); err != nil {
				return nil, err
			}
		}
	}
	if err := writeBucket(overlayPath(dst, from), liveBucket(merged, dead)); err != nil {
		return nil, err
	}
	if inPlace {
		return newestFirst(dst, from+1, len(layers)), nil
	}
	return nil, nil
}

// newestFirst is the overlays numbered first to last of the bucket at base,
// newest first.
func newestFirst(base string, first, last int) []string {
	var out []string
	for i := last; i >= first; i-- {
		out = append(out, overlayPath(base, i))
	}
	return out
}

// staleCount is how many files stale names.
func staleCount(stale [][]string) int {
	n := 0
	for _, s := range stale {
		n += len(s)
	}
	return n
}

// dropStale removes the overlays an in-place merge made redundant, each
// bucket's newest first. A file that will not go (Windows, a reader holding
// it) stops its bucket there: what is left repeats postings the merged file
// holds, which a read joins away and the next merge takes in, and no gap
// opens below it. It runs after the manifest commits, which counted the
// files without these: before, a reader's damage check would see fewer
// files than the manifest it read promised (#5426).
func dropStale(stale [][]string) {
	for _, files := range stale {
		for _, p := range files {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				break
			}
		}
	}
}

// maxPostingBytes bounds one encoded posting: two varints, of an offset
// difference and of a session id with the tool bit.
const maxPostingBytes = binary.MaxVarintLen64 + binary.MaxVarintLen32 + 1

// blankDeadWords overwrites, in the postings files of the index at dir, every
// word of words (the dead records' postings by bucket) that no live record
// holds any more. The bytes of the word in the file's directory past the
// bucket's own letters become zeros, in place: the entry keeps its length and
// its block, which holds only dead records' postings, and no query names a
// word of zeros. A block too long to
// be dead records' alone is a live word, known without decoding it. Rewriting
// the file without the word instead cost a whole bucket per word, and the
// buckets of a text index are its first letter (#5429). It returns the words
// it blanked.
func blankDeadWords(dir string, words bucketPostings, dead []int64) (map[string]bool, error) {
	names := make([]string, 0, len(words))
	for b := range words {
		names = append(names, b)
	}
	// A bucket at a time on a few workers, as addOverlays writes them: each
	// one ends in an fsync, and one after another they were most of what a
	// small pass spent here.
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		failed error
		gone   = map[string]bool{}
	)
	work := make(chan string)
	for range min(runtime.NumCPU(), 8, len(names)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range work {
				blanked, err := blankDeadBucketWords(filepath.Join(dir, "buckets", b+".bin"), words[b], dead)
				mu.Lock()
				failed = cmp.Or(failed, err)
				for w := range blanked {
					gone[w] = true
				}
				mu.Unlock()
			}
		}()
	}
	for _, b := range names {
		work <- b
	}
	close(work)
	wg.Wait()
	return gone, failed
}

// blankDeadBucketWords is blankDeadWords for the bucket at base and its
// overlays.
func blankDeadBucketWords(base string, toks map[string][]posting, dead []int64) (map[string]bool, error) {
	files := append([]string{base}, bucketLayers(base)...)
	live := map[string]bool{}
	// Newest first: the pass that killed these records wrote the session's
	// new ones into the newest overlay, and a word found live in that small
	// file is not looked up in the bucket.
	for _, p := range slices.Backward(files) {
		if err := markLiveWords(p, toks, dead, live); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	gone := map[string]bool{}
	for tok := range toks {
		if !live[tok] {
			gone[tok] = true
		}
	}
	if len(gone) == 0 {
		return nil, nil
	}
	for _, p := range files {
		if err := blankWords(p, gone); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return gone, nil
}

// liveProbeBytes is how much of a word's postings is read first when asking
// whether it is still live.
const liveProbeBytes = 256

// postingBytesProbed counts the postings bytes markLiveWords read, for the
// test that keeps the scrub from reading a word's whole block.
var postingBytesProbed atomic.Int64

// markLiveWords sets live[tok] for each word of toks the file at p holds a
// live posting of.
func markLiveWords(p string, toks map[string][]posting, dead []int64, live map[string]bool) error {
	entries, f, err := openBucketDir(p)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var buf []byte
	for _, e := range entries {
		if _, ok := toks[e.tok]; !ok || live[e.tok] {
			continue
		}
		if int(e.n) > maxPostingBytes*len(dead) {
			live[e.tok] = true
			continue
		}
		// One live posting settles it, and a word the dead records shared
		// with the rest of the store has one near the start of its block:
		// reading the whole block cost a common word's postings each time.
		n := min(int(e.n), liveProbeBytes)
		for {
			if cap(buf) < n {
				buf = make([]byte, n)
			}
			b := buf[:n]
			if _, err := f.ReadAt(b, int64(e.off)); err != nil {
				return err
			}
			postingBytesProbed.Add(int64(n))
			if len(liveOnly(decodePostings(b), dead)) > 0 {
				live[e.tok] = true
				break
			}
			if n == int(e.n) {
				break
			}
			n = int(e.n)
		}
	}
	return nil
}

// blankWords zeroes the words of gone where the directory of the postings
// file at p names them, and syncs the file.
func blankWords(p string, gone map[string]bool) error {
	f, err := os.OpenFile(p, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(f)
	magic := make([]byte, len(bucketMagic))
	if _, err := io.ReadFull(r, magic); err != nil || !bytes.Equal(magic, bucketMagic) {
		return errCorruptIndex
	}
	count, err := binary.ReadUvarint(r)
	if err != nil {
		return err
	}
	pos := int64(len(bucketMagic) + uvarintLen(count))
	type span struct {
		at    int64
		blank []byte
	}
	var hits []span
	tb := []byte{}
	for i := uint64(0); i < count; i++ {
		ln, err := binary.ReadUvarint(r)
		if err != nil {
			return err
		}
		pos += int64(uvarintLen(ln))
		if ln > uint64(maxRecordSize) {
			return errCorruptIndex
		}
		if cap(tb) < int(ln) {
			tb = make([]byte, ln)
		}
		tb = tb[:ln]
		if _, err := io.ReadFull(r, tb); err != nil {
			return err
		}
		if gone[string(tb)] {
			if b, ok := blankedWord(string(tb)); ok {
				hits = append(hits, span{pos, b})
			}
		}
		pos += int64(ln)
		n, err := binary.ReadUvarint(r)
		if err != nil {
			return err
		}
		pos += int64(uvarintLen(n))
	}
	if len(hits) == 0 {
		return nil
	}
	for _, h := range hits {
		if _, err := f.WriteAt(h.blank, h.at); err != nil {
			return err
		}
	}
	return f.Sync()
}

// blankedWord is tok overwritten, at its length, so it stays filed in the
// bucket it is in and keeps no more of itself than the bucket's name says.
// A bucket named after two ASCII letters keeps those and zeroes the rest.
// One named by a hash of the leading runes, as every CJK key's is, says
// nothing of them, so the word is replaced whole by filler hashing to the
// same bucket (#5452): an invalid byte, which keeps it off the ASCII buckets
// and apart from any real word, and control bytes. ok is false for a word
// with no such filler: only a one-letter two-byte word, whose filler has two
// bytes to hash and reaches most buckets but not all.
func blankedWord(tok string) (_ []byte, ok bool) {
	want := bucket(tok)
	b := make([]byte, len(tok))
	if len(tok) >= 2 && want == safe(tok[:2]) {
		copy(b, tok[:2])
		return b, true
	}
	switch {
	case len(b) >= 3:
		f, ok := hashedFillers()[want]
		if !ok {
			return nil, false
		}
		copy(b, f[:])
		return b, true
	case len(b) == 2:
		for c := byte(0); c < utf8.RuneSelf; c++ {
			for _, f := range [][]byte{{0xff, c}, {c, 0xff}} {
				if bucket(string(f)) == want {
					return f, true
				}
			}
		}
	}
	return nil, false
}

// hashedFillers is, by hashed bucket, three bytes that, followed by zeros,
// file a word there: only the first three runes are hashed.
var hashedFillers = sync.OnceValue(func() map[string][3]byte {
	out := make(map[string][3]byte, len(hexBuckets))
	for c1 := byte(0); c1 < utf8.RuneSelf && len(out) < len(hexBuckets); c1++ {
		for c2 := byte(0); c2 < utf8.RuneSelf; c2++ {
			f := [3]byte{0xff, c1, c2}
			if name := bucket(string(f[:])); out[name] == ([3]byte{}) {
				out[name] = f
			}
		}
	}
	return out
})

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
