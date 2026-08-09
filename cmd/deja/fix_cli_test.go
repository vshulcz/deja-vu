package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An unknown flag must not be swallowed into the error text, and --limit must
// not silently corrupt the query when its value is missing.
func TestFixRejectsFlagMistakes(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "s.jsonl"), "s", []string{
		`{"type":"user","sessionId":"s","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"hi"}}`,
	})
	if _, err := captureRun(t, "fix", "boom", "--json"); err == nil {
		t.Error("unknown flag --json was accepted and folded into the query")
	}
	if _, err := captureRun(t, "fix", "boom", "--limit"); err == nil {
		t.Error("--limit with no value was accepted")
	}
}

// The empty result never accuses the user of not pasting an error: the paste is
// exactly the output people give (`Error: …`, `npm ERR! …`), and the old check
// rejected all of it.
func TestFixDoesNotCallARealErrorNotAnError(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "s.jsonl"), "s", []string{
		`{"type":"user","sessionId":"s","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"hi"}}`,
	})
	for _, in := range []string{"Error: Cannot find module 'express'", "npm ERR! code E404"} {
		out, err := captureRun(t, "fix", in)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "does not read like an error") {
			t.Errorf("fix told the user their pasted error is not an error: %q → %q", in, out)
		}
		if !strings.Contains(out, "no session") {
			t.Errorf("expected the neutral empty line for %q, got %q", in, out)
		}
	}
}
