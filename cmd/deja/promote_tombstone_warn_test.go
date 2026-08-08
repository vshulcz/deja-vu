package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Promoting a forgotten session writes the note back, but a tombstone lifts
// only through unforget by design — so the note stayed suppressed and promote
// reported success over a note that never appeared. It now says so and names
// the restore command; an ordinary promote stays quiet.
func TestPromoteWarnsWhenTheNoteIsTombstoned(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(tmp, "claude", "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	write := func(id, body string) {
		line := `{"type":"user","sessionId":"` + id + `","cwd":"/w/p","timestamp":"2026-06-01T10:00:00Z","message":{"role":"user","content":"` + body + `"}}`
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("c1", "forgotten then repromoted")
	write("c2", "never forgotten")
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	// Promote, forget the note, then promote the same source again.
	if _, err := captureRunStderr(t, "promote", "c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "forget", "--session", "deja-note-claude-c1"); err != nil {
		t.Fatal(err)
	}
	warn, err := captureRunStderr(t, "promote", "c1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warn, "stays hidden") || !strings.Contains(warn, "unforget deja:deja-note-claude-c1") {
		t.Errorf("re-promote of a forgotten note did not warn:\n%s", warn)
	}

	// An ordinary promote of a session that was never forgotten stays quiet.
	quiet, err := captureRunStderr(t, "promote", "c2")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(quiet, "stays hidden") {
		t.Errorf("ordinary promote warned about a tombstone it does not have:\n%s", quiet)
	}
}
