package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// "older than 0 days" is the whole store, and the typo that produces it is 0
// for 30. A negative duration filters nothing in `last` while deleting
// everything here (#739).
func TestForgetBeforeRejectsNonPositiveAges(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"a1","cwd":"/w/p","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"work about the pool"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	for _, age := range []string{"0d", "0", "0h", "-5d", "-1h"} {
		_, err := captureRun(t, "forget", "--before", age, "--dry-run")
		if err == nil {
			t.Errorf("--before %s was accepted", age)
			continue
		}
		if !strings.Contains(err.Error(), "which is every session") {
			t.Errorf("--before %s: %v", age, err)
		}
	}
	// Real ages still work, including sub-day ones.
	for _, age := range []string{"30d", "1h", "90m", "1s"} {
		if _, err := captureRun(t, "forget", "--before", age, "--dry-run"); err != nil {
			t.Errorf("--before %s: %v", age, err)
		}
	}
	// A date is a different form and keeps its own error.
	if _, err := captureRun(t, "forget", "--before", "2026-01-01", "--dry-run"); err != nil {
		t.Errorf("date form: %v", err)
	}
	if _, err := captureRun(t, "forget", "--before", "yesterdayish", "--dry-run"); err == nil ||
		!strings.Contains(err.Error(), "neither a duration nor a date") {
		t.Errorf("garbage form: %v", err)
	}
}
