package index

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
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
	cat map[string]bool
	ok  bool
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
	sig, err := bucketsSignature(dir)
	if err != nil {
		return tokenCatalog(dir)
	}
	catalogCache.mu.Lock()
	if catalogCache.ok && catalogCache.dir == dir && catalogCache.sig == sig {
		c := catalogCache.cat
		catalogCache.mu.Unlock()
		return c, nil
	}
	catalogCache.mu.Unlock()
	c, err := tokenCatalog(dir)
	if err != nil {
		return nil, err
	}
	catalogCache.mu.Lock()
	catalogCache.dir, catalogCache.sig = dir, sig
	catalogCache.cat, catalogCache.ok = c, true
	catalogCache.mu.Unlock()
	return c, nil
}
