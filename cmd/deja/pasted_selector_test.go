package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// An id arrives from a chat wrapped in quotes or backticks, and off deja's own
// screen with the harness in front of it: `forget --list`, the undo line beside
// it and promote's receipts all print `harness:id`. `show` refused that form
// and `forget --session` accepted it while matching nothing (#921).
func TestAPastedIdIsAccepted(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "a1b2c3d4-1111-4000-8000-d6e7f8a9b0c1"
	rec := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-10T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	for _, sel := range []string{
		id,
		"claude:" + id,
		`"` + id + `"`,
		"'" + id + "'",
		"`claude:" + id + "`",
		"  " + id + "  ",
	} {
		out, err := captureRun(t, "show", sel)
		if err != nil {
			t.Errorf("show %q: %v", sel, err)
			continue
		}
		if !strings.Contains(out, id) {
			t.Errorf("show %q found nothing:\n%s", sel, out)
		}
	}

	// The harness in front of the id is honoured, not ignored: another
	// harness's name means another session.
	if _, err := captureRun(t, "show", "codex:"+id); err == nil {
		t.Error("show accepted an id under the wrong harness")
	}

	// And the same shape selects for forget rather than silently matching
	// nothing.
	out, err := captureRun(t, "forget", "--session", "claude:"+id, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "would drop: 1 session(s)") {
		t.Errorf("forget --session harness:id matched nothing:\n%s", out)
	}
}
