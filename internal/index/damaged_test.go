package index

import (
	"os"
	"path/filepath"
	"testing"
)

// The next search rebuilds a damaged store, so nothing is lost permanently —
// what was wrong is the answer doctor gave: "built, up to date" about a store
// that could not return a single result (#735).
func TestDamaged(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","sessionId":"s1","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"the retry storm"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if Damaged(dir) {
		t.Fatal("a freshly built index reported damage")
	}

	// Postings gone: a partial copy, an interrupted sync, a find -delete.
	buckets := filepath.Join(dir, "buckets")
	saved := filepath.Join(tmp, "buckets.bak")
	if err := os.Rename(buckets, saved); err != nil {
		t.Fatal(err)
	}
	if !Damaged(dir) {
		t.Error("missing postings not reported")
	}
	if err := os.Rename(saved, buckets); err != nil {
		t.Fatal(err)
	}
	if Damaged(dir) {
		t.Error("restored postings still reported as damage")
	}

	// A truncated record log.
	if err := os.Truncate(filepath.Join(dir, "records.bin"), 10); err != nil {
		t.Fatal(err)
	}
	if !Damaged(dir) {
		t.Error("truncated record log not reported")
	}

	// No index at all is "not built", which doctor reports on its own.
	if Damaged(filepath.Join(tmp, "no-such-index")) {
		t.Error("a missing index reported damage")
	}
}
