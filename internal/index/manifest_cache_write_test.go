package index

import (
	"os"
	"path/filepath"
	"testing"
)

// The manifest cache is keyed on manifest.gob's mtime and size, which its own
// comment called a pair the atomic swap always changes. It is not: a rewrite
// that keeps the size and lands inside one tick of the filesystem's timestamp
// resolution leaves both unchanged, and every read-only surface in the process
// — doctor's read state, the session count, friction, the brief — then answers
// from the manifest before it. This is the shape that made
// TestReadStateOf fail on CI and pass everywhere else: six manifests written
// in a loop, two of them the same size.
//
// Reproduced deterministically by putting the second write back on the first
// one's timestamp, which is what a coarse clock does on its own.
func TestAManifestRewriteIsVisibleEvenWhenMtimeAndSizeRepeat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.gob")

	first := Manifest{Version: version, Format: onDiskFormat, Sessions: map[string]SessionMeta{}}
	if err := writeManifest(dir, first); err != nil {
		t.Fatal(err)
	}
	if got := ReadStateOf(dir); got != ReadStateCurrent {
		t.Fatalf("the first manifest reads as %v, want ReadStateCurrent", got)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	// Same encoded size (one integer differs, same number of digits), then the
	// clock put back where it was.
	second := Manifest{Version: version, Format: onDiskFormat + 1, Sessions: map[string]SessionMeta{}}
	if err := writeManifest(dir, second); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	if again, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if again.Size() != fi.Size() || !again.ModTime().Equal(fi.ModTime()) {
		t.Skipf("the two manifests differ in size (%d against %d) — the collision this test needs is gone",
			again.Size(), fi.Size())
	}

	if got := ReadStateOf(dir); got != ReadStateUnreadable {
		t.Errorf("after a rewrite the store reads as %v, want ReadStateUnreadable — the cache answered from the manifest before it", got)
	}
	// And the cached manifest itself, not only the state derived from it.
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Format != onDiskFormat+1 {
		t.Errorf("the cached manifest says format %d, want %d", m.Format, onDiskFormat+1)
	}
}

// The cache still has to be a cache: a read that follows a read with no write
// between them must not decode the manifest again.
func TestTheManifestCacheStillServesRepeatedReads(t *testing.T) {
	dir := t.TempDir()
	if err := writeManifest(dir, Manifest{Version: version, Format: onDiskFormat, Sessions: map[string]SessionMeta{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := readManifestCached(dir); err != nil {
		t.Fatal(err)
	}
	// Remove the file the decode reads. A cached answer survives it; a
	// re-decode falls back to readManifest and fails.
	if err := os.Remove(filepath.Join(dir, "sessions.gob")); err != nil {
		t.Fatal(err)
	}
	// Keep manifest.gob's stamp where the cache saw it.
	before, err := os.Stat(filepath.Join(dir, "manifest.gob"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "manifest.gob"), before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatalf("the second read did not come from the cache: %v", err)
	}
	if m.Version != version {
		t.Errorf("cached manifest version = %d, want %d", m.Version, version)
	}
}
