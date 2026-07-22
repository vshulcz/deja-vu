package index

import (
	"os"
	"path/filepath"
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
