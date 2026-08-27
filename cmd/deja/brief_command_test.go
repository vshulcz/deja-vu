package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The first screen printed only when stdout was a terminal, so `deja | less`
// and `deja > file` got the usage screen and there was no way to ask for it on
// purpose — the obvious guess was a search for the word (#2108).
func TestBriefIsACommandAndPrintsIntoAPipe(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := claudeRecord(t, map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		"message":   map[string]any{"role": "user", "content": "the pool was exhausted while the migration held the lock"},
	})
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// captureRun's stdout is a pipe, which is the case this is about.
	out, err := captureRun(t, "brief")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"across 1 agent", "recent", "the pool was exhausted"} {
		if !strings.Contains(out, want) {
			t.Errorf("the first screen does not carry %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "no matches") || strings.Contains(out, "Usage:") {
		t.Errorf("`deja brief` did not print the first screen:\n%s", out)
	}
}
