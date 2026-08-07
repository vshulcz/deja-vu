package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// ctx answered a misspelling from the close tier and said nothing: the agent
// got a session about a word it never typed with no way to tell. The search
// screen has narrated every rung of the ladder all along. It goes on stderr —
// stdout stays the context block an agent parses (#R14).
func TestCtxSaysWhichRungAnswered(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"the invoice reconciler double counts refunds"},"timestamp":"2026-08-04T10:00:00Z","sessionId":"w14","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", false, nil); err != nil {
		t.Fatal(err)
	}

	note, err := captureRunStderr(t, "ctx", "reconcilar")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "reconcilar -> reconciler") {
		t.Errorf("ctx substituted a word without saying so: %q", note)
	}

	out, err := captureRun(t, "ctx", "reconcilar")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "# deja context:") {
		t.Errorf("the hint reached stdout, where an agent parses the block:\n%s", out)
	}
}
