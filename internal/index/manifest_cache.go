package index

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

// Long-lived processes (the MCP server foremost) call read-only retrieval
// dozens of times per session; decoding the manifest and a thousand session
// metas on every call is pure waste. The cache is keyed by manifest.gob's
// mtime+size, which the atomic index swap always changes.
//
// Contract: the cached Manifest is shared — read-only paths must not mutate
// it. Ingestion keeps using readManifest directly.
var manifestCache struct {
	mu    sync.Mutex
	dir   string
	mtime time.Time
	size  int64
	m     Manifest
	ok    bool
}

func readManifestCached(dir string) (Manifest, error) {
	// Plain stat: a failure here falls through to readManifest, which waits the
	// swap out itself, so a wait added here cannot be made to fail (#1319).
	fi, err := os.Stat(filepath.Join(dir, "manifest.gob"))
	if err != nil {
		return readManifest(dir)
	}
	manifestCache.mu.Lock()
	if manifestCache.ok && manifestCache.dir == dir &&
		manifestCache.mtime.Equal(fi.ModTime()) && manifestCache.size == fi.Size() {
		m := manifestCache.m
		manifestCache.mu.Unlock()
		return m, nil
	}
	manifestCache.mu.Unlock()
	m, err := readManifest(dir)
	if err != nil {
		return m, err
	}
	manifestCache.mu.Lock()
	manifestCache.dir, manifestCache.mtime, manifestCache.size = dir, fi.ModTime(), fi.Size()
	manifestCache.m, manifestCache.ok = m, true
	manifestCache.mu.Unlock()
	return m, nil
}

// The token catalog is every distinct token in the corpus — 180k strings on a
// real store. The stem and fuzzy tiers each build it, so a single query that
// falls through the ladder built it twice, ~63 ms of pure duplication.
//
// Keyed on the bucket files themselves, not the manifest: a bucket can be
// corrupted without the manifest changing, and tokenCatalog reporting that
// corruption is what makes the search ladder rebuild. A manifest-keyed cache
// would have hidden it.
//
// Contract: the returned map is shared and must not be mutated.
var catalogCache struct {
	mu  sync.Mutex
	dir string
	sig string
	idx *tokenIndex
	ok  bool
}

// tokenIndex is the catalog plus the same tokens bucketed by rune length.
// Fuzzy matching only ever considers tokens within its edit limit of the
// query's length, so bucketing turns a scan of every token in the corpus into
// a walk of a few short slices.
type tokenIndex struct {
	set   map[string]bool
	byLen [][]string
	// Tokens carrying a combining mark, bucketed by the length they have
	// without it. The close tier treats a mark as free, and a marked token is
	// as many runes longer than its unmarked form as it has marks, so the
	// ordinary length window never reaches it (#1941). Marked tokens are a
	// small minority of any corpus, so this second bucket list costs little.
	markedByLen [][]string
}

const maxIndexedTokenLen = 64

func newTokenIndex(set map[string]bool) *tokenIndex {
	idx := &tokenIndex{
		set:         set,
		byLen:       make([][]string, maxIndexedTokenLen+2),
		markedByLen: make([][]string, maxIndexedTokenLen+2),
	}
	for tok := range set {
		n := len(tok)
		ascii := isASCIIString(tok)
		if !ascii {
			n = utf8.RuneCountInString(tok)
		}
		idx.byLen[bucketFor(n)] = append(idx.byLen[bucketFor(n)], tok)
		if ascii {
			continue // no ASCII byte is a combining mark
		}
		if bare, marked := unmarked(tok); marked {
			b := bucketFor(utf8.RuneCountInString(bare))
			idx.markedByLen[b] = append(idx.markedByLen[b], tok)
		}
	}
	return idx
}

// bucketFor clamps a rune length to the bucket that holds it; anything longer
// than maxIndexedTokenLen shares one overflow bucket, which is always scanned.
func bucketFor(n int) int {
	if n > maxIndexedTokenLen {
		return maxIndexedTokenLen + 1
	}
	return n
}

// candidates visits the tokens whose rune length is within limit of n, plus
// the overflow bucket, which holds tokens too long to bucket exactly.
func (t *tokenIndex) candidates(n, limit int, fn func(string)) {
	lo, hi := n-limit, n+limit
	if lo < 0 {
		lo = 0
	}
	if hi > maxIndexedTokenLen {
		hi = maxIndexedTokenLen
	}
	for l := lo; l <= hi && l < len(t.byLen); l++ {
		for _, tok := range t.byLen[l] {
			fn(tok)
		}
	}
	for _, tok := range t.byLen[maxIndexedTokenLen+1] {
		fn(tok)
	}
}

// markedCandidates visits the tokens whose length without their combining
// marks is within limit of n. The close tier compares those in the same
// unmarked form, so a query typed without marks reaches the word written with
// them however many marks it carries.
func (t *tokenIndex) markedCandidates(n, limit int, fn func(string)) {
	lo, hi := n-limit, n+limit
	if lo < 0 {
		lo = 0
	}
	if hi > maxIndexedTokenLen {
		hi = maxIndexedTokenLen
	}
	for l := lo; l <= hi && l < len(t.markedByLen); l++ {
		for _, tok := range t.markedByLen[l] {
			fn(tok)
		}
	}
	for _, tok := range t.markedByLen[maxIndexedTokenLen+1] {
		fn(tok)
	}
}

// hasMarkedNear reports whether any token carrying a combining mark has an
// unmarked length within limit of n. Cheap enough to ask before deciding
// whether a short term is worth the candidate walk.
func (t *tokenIndex) hasMarkedNear(n, limit int) bool {
	lo, hi := n-limit, n+limit
	if lo < 0 {
		lo = 0
	}
	if hi > maxIndexedTokenLen {
		hi = maxIndexedTokenLen
	}
	for l := lo; l <= hi && l < len(t.markedByLen); l++ {
		if len(t.markedByLen[l]) > 0 {
			return true
		}
	}
	return len(t.markedByLen[maxIndexedTokenLen+1]) > 0
}

// bucketsSignature changes whenever any bucket file is added, removed, resized
// or rewritten. Stat-only, so it costs a fraction of building the catalog.
func bucketsSignature(dir string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, "buckets"))
	if err != nil {
		return "", err
	}
	h := fnv.New64a()
	for _, de := range entries {
		fi, err := de.Info()
		if err != nil {
			return "", err
		}
		_, _ = h.Write([]byte(de.Name()))
		_, _ = fmt.Fprintf(h, "|%d|%d;", fi.Size(), fi.ModTime().UnixNano())
	}
	return strconv.FormatUint(h.Sum64(), 16), nil
}

func tokenCatalogCached(dir string) (map[string]bool, error) {
	idx, err := tokenIndexCached(dir)
	if err != nil {
		return nil, err
	}
	return idx.set, nil
}

func tokenIndexCached(dir string) (*tokenIndex, error) {
	sig, err := bucketsSignature(dir)
	if err != nil {
		c, cerr := tokenCatalog(dir)
		if cerr != nil {
			return nil, cerr
		}
		return newTokenIndex(c), nil
	}
	catalogCache.mu.Lock()
	if catalogCache.ok && catalogCache.dir == dir && catalogCache.sig == sig {
		i := catalogCache.idx
		catalogCache.mu.Unlock()
		return i, nil
	}
	catalogCache.mu.Unlock()
	c, err := tokenCatalog(dir)
	if err != nil {
		return nil, err
	}
	i := newTokenIndex(c)
	catalogCache.mu.Lock()
	catalogCache.dir, catalogCache.sig = dir, sig
	catalogCache.idx, catalogCache.ok = i, true
	catalogCache.mu.Unlock()
	return i, nil
}
