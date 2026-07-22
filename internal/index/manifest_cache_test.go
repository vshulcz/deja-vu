package index

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManifestCacheInvalidatesOnReindex(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"c1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"first session"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "c1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m1, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m1.Sessions) != 1 {
		t.Fatalf("sessions = %d", len(m1.Sessions))
	}
	// Cached call returns the same view.
	if m2, _ := readManifestCached(dir); len(m2.Sessions) != 1 {
		t.Fatal("cache hit lost sessions")
	}
	// Reindex swaps manifest.gob; the cache must notice the new mtime/size.
	line2 := `{"type":"user","sessionId":"c2","timestamp":"2026-01-03T03:04:05Z","message":{"role":"user","content":"second session"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "c2.jsonl"), []byte(line2), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m3, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m3.Sessions) != 2 {
		t.Fatalf("cache served stale manifest: %d sessions", len(m3.Sessions))
	}
}
