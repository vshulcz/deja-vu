package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// recall attaches a correction to the session it belongs to (#684, #694); the
// two paths that inject unprompted did not — the block listed the note as a
// separate item and hook-prompt served a rejected session as an equal answer
// (#761).
func TestOrderForInjection(t *testing.T) {
	tmp := t.TempDir()
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	body := `{"ts":"2026-08-01T10:00:00Z","project":"p","text":"nitrile swelled, do not use","kind":"promoted","session":"claude:bad","state":"rejected"}` + "\n"
	if err := os.WriteFile(notes, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ss := []model.Session{
		{Harness: "claude", ID: "bad", Project: "p"},
		{Harness: "claude", ID: "good", Project: "p"},
	}
	got, warn := orderForInjection(ss)
	if got[0].ID != "good" || got[1].ID != "bad" {
		t.Errorf("order = %s, %s", got[0].ID, got[1].ID)
	}
	if !strings.Contains(warn, "session bad was tried and rejected") || !strings.Contains(warn, "nitrile swelled") {
		t.Errorf("warning = %q", warn)
	}

	// Nothing marked: no reordering, no warning. A line on every injection is
	// noise the agent learns to skip.
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "empty.jsonl"))
	got, warn = orderForInjection(ss)
	if got[0].ID != "bad" || warn != "" {
		t.Errorf("clean store: order %s, warn %q", got[0].ID, warn)
	}

	// Other states are not rejections: superseded says "this was true once",
	// which is still the record of how the answer was reached.
	t.Setenv("DEJA_NOTES_FILE", notes)
	if err := os.WriteFile(notes, []byte(strings.Replace(body, `"rejected"`, `"superseded"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	got, warn = orderForInjection(ss)
	if got[0].ID != "bad" || warn != "" {
		t.Errorf("superseded: order %s, warn %q", got[0].ID, warn)
	}
}
