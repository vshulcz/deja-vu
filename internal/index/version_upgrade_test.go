package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	search "github.com/vshulcz/deja-vu/internal/query"
)

// Version 12 reshards non-ASCII tokens, so an index left at an older version
// must be rebuilt before it is searched — and every script must survive that
// rebuild. (This covers the upgrade path, not the shape of a genuine v11
// store: several tiers enumerate bucket directories directly, so a v11 store
// stays partly reachable rather than going dark.)
func TestOlderIndexVersionIsRebuiltBeforeSearch(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"обсуждали миграцию базы и 数据库迁移 plus asciimarker"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// Pretend this index was written by the previous version.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Version = 11
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	// The manifest cache keys on mtime+size, and a version field is the same
	// size either way — move the mtime so the cache cannot serve the old copy.
	tick := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(dir, "manifest.gob"), tick, tick); err != nil {
		t.Fatal(err)
	}
	if err := EnsureForSearch(dir, search.Options{Query: "миграцию", All: true}, false, nil); err != nil {
		t.Fatal(err)
	}
	got, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != version {
		t.Fatalf("index still at version %d — a stale-version index was served, not rebuilt", got.Version)
	}
	for _, q := range []string{"миграцию", "数据库迁移", "asciimarker"} {
		ss, err := Search(dir, search.Options{Query: q, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) == 0 {
			t.Errorf("query %q found nothing after the version upgrade", q)
		}
	}
}

// Hook paths never call Ensure, so they must be able to tell an older store
// apart and ask for a rebuild instead of reading it. Reading a v11 store with
// v12 code returns nothing for Russian and CJK while English still works —
// silent, and the user has no way to see why.
func TestIsCurrentVersionDetectsOlderStore(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"обсуждали миграцию базы"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if !IsCurrentVersion(dir) {
		t.Fatal("a freshly built index must report as current")
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Version = version - 1
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if IsCurrentVersion(dir) {
		t.Fatal("an older store reported as current — hook paths would read it and silently recall nothing")
	}
	// And a store that was never built is not "current" either.
	if IsCurrentVersion(filepath.Join(tmp, "nope")) {
		t.Fatal("a missing index reported as current")
	}
}
