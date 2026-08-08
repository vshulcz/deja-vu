package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// forget --unforget --dry-run brought the session back anyway: the destructive
// side runs a dry probe and never touches disk, but the undo went straight to
// Unforget, which lifts the tombstone and rebuilds (#1066).
func TestUnforgetDryRunRestoresNothing(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"UNDRY dry run undo"},"timestamp":"2026-06-01T10:00:00Z","sessionId":"u","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "u.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "forget", "--session", "claude:u"); err != nil {
		t.Fatal(err)
	}
	if n := index.TombstoneMatches("claude:u"); n != 1 {
		t.Fatalf("forgot %d sessions, want 1", n)
	}

	// The dry run names what it would restore and changes nothing.
	out, err := captureRun(t, "forget", "--unforget", "claude:u", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nothing was changed") || !strings.Contains(out, "would restore") {
		t.Errorf("dry run does not say it changed nothing: %q", out)
	}
	if n := index.TombstoneMatches("claude:u"); n != 1 {
		t.Errorf("the dry run lifted the tombstone: %d left, want 1", n)
	}

	// The real undo still restores it.
	if _, err := captureRunStderr(t, "forget", "--unforget", "claude:u"); err != nil {
		t.Fatal(err)
	}
	if n := index.TombstoneMatches("claude:u"); n != 0 {
		t.Errorf("the real undo left %d tombstones, want 0", n)
	}
}
