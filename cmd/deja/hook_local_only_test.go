package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

// DEJA_AUTORECALL_LOCAL_ONLY=1 keeps synced sessions out of the startup
// digest while leaving them searchable.
func TestAutoRecallLocalOnly(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-isoproj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	local := `{"type":"user","sessionId":"loc1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"local isoproj work"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "loc1.jsonl"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.EnsureForSearch(dir, search.Options{Query: "isoproj"}, false, nil); err != nil {
		t.Fatal(err)
	}
	batch := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(batch, "deja-sync-x-1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	_ = enc.Encode(index.SyncRecord{Harness: "claude", SessionID: "rem1", Project: "tmp/isoproj", Role: "user", Text: "remote isoproj work", Time: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)})
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := index.Import(dir, batch); err != nil || n != 1 {
		t.Fatalf("import n=%d err=%v", n, err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/tmp/isoproj")

	digest := hookDigest()
	if !strings.Contains(digest, "imported:") {
		t.Fatalf("default digest should include imported session, got: %q", digest)
	}
	t.Setenv("DEJA_AUTORECALL_LOCAL_ONLY", "1")
	digest = hookDigest()
	if strings.Contains(digest, "imported:") {
		t.Fatalf("local-only digest leaked imported session: %q", digest)
	}
	if !strings.Contains(digest, "isoproj") {
		t.Fatalf("local-only digest lost local session: %q", digest)
	}
}
