package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

func TestCachedHookDigestServesWithinTTLAndInvalidates(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-proj", "one.jsonl"), "one", []string{
		`{"type":"user","sessionId":"one","timestamp":"` + old + `","message":{"role":"user","content":"the original digest content marker_alpha"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "proj")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)

	d1, s1, _, _ := cachedHookDigest(dir)
	if d1 == "" || s1 == 0 {
		t.Fatal("first call must build a digest")
	}
	// New work lands, index updated — but within the TTL the hook must serve
	// the cached digest without touching the index.
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-proj", "two.jsonl"), "two", []string{
		`{"type":"user","sessionId":"two","timestamp":"` + time.Now().Add(-25*time.Hour).UTC().Format(time.RFC3339) + `","message":{"role":"user","content":"a fresher session marker_beta"}}`,
	})
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	d2, _, _, _ := cachedHookDigest(dir)
	if d2 != d1 {
		t.Fatal("within TTL the cached digest must be served verbatim")
	}
	// Expire the cache: startup must still serve the stale digest instantly
	// (stale-while-revalidate) — freshness arrives via the detached refresh.
	cachePath := hookCachePath(dir, cwd)
	var e hookCacheEntry
	b, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatal(err)
	}
	e.At = time.Now().Add(-2 * hookDigestTTL)
	nb, _ := json.Marshal(e)
	if err := os.WriteFile(cachePath, nb, 0o600); err != nil {
		t.Fatal(err)
	}
	d3, _, _, _ := cachedHookDigest(dir)
	if d3 != d1 {
		t.Fatalf("expired cache must serve stale instantly, got:\n%s", d3)
	}
	// The refresh itself must produce the fresh view.
	runHookRefresh(dir)
	d3b, _, _, _ := cachedHookDigest(dir)
	if !strings.Contains(d3b, "marker_beta") {
		t.Fatalf("refresh must rebuild with fresh sessions:\n%s", d3b)
	}
	d3 = d3b
	// A different cwd must never reuse another project's cache.
	t.Setenv("CLAUDE_PROJECT_DIR", filepath.Join(t.TempDir(), "elsewhere"))
	d4, _, _, _ := cachedHookDigest(dir)
	if d4 == d3 && d3 != "" {
		t.Fatal("cache must be scoped to cwd")
	}
}

func TestHookContextNeverRebuildsIndex(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	// A fresh session so the handoff-tip path (the one that used to index
	// synchronously) actually engages.
	fresh := time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-app", "one.jsonl"), "one", []string{
		`{"type":"user","sessionId":"one","timestamp":"` + fresh + `","message":{"role":"user","content":"recent work marker"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)
	// Dirty the store: a new session file appears after the index was built.
	late := `{"type":"user","sessionId":"late1","timestamp":"` + fresh + `","message":{"role":"user","content":"late arrival"}}` + "\n"
	if err := os.WriteFile(filepath.Join(claudeRoot, "-tmp-app", "late1.jsonl"), []byte(late), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runHookContext(dir, true); err != nil {
		t.Fatal(err)
	}
	// If the hook indexed, the late session is now findable; startup must
	// never pay for indexing, so it must not be.
	if _, ok, _ := index.FindByPrefix(dir, "late1"); ok {
		t.Fatal("session-start hook indexed a dirty store — startup must never pay for indexing")
	}
}
