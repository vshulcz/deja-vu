package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A note the user asked to forget has to lose its text, not just its place in
// the index: a tombstone silences search while the line stays in notes.jsonl,
// which deja wrote (#841).
func TestForgettingANoteRemovesItsLine(t *testing.T) {
	hermeticEnv(t)
	notesDir := filepath.Join(t.TempDir(), "notes")
	if err := os.MkdirAll(notesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(notesDir, "notes.jsonl"))
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, text string }{
		{"s1", "the acme corp renewal price"},
		{"s2", "the winch brake pads"},
	} {
		line := `{"type":"user","message":{"role":"user","content":"` + tc.text + `"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"` + tc.id + `","cwd":"/proj"}`
		if err := os.WriteFile(filepath.Join(store, tc.id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "promote", "s1", "--state", "accepted", "--note", "acme renewal agreed at 240k"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "promote", "s2", "--state", "accepted", "--note", "brake pads replaced"); err != nil {
		t.Fatal(err)
	}

	// Forgetting the source keeps the note — the decision is often why the raw
	// session was safe to forget (#666) — but must say the text is still there.
	out, err := captureRun(t, "forget", "--session", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "still holds what you wrote there") {
		t.Errorf("forgetting the source does not say the note kept its content:\n%s", out)
	}
	body, err := os.ReadFile(sources.NotesFile())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "240k") {
		t.Fatalf("the note lost its content when only the source was forgotten:\n%s", body)
	}

	// Forgetting the note itself removes the line, and leaves the other note.
	out, err = captureRun(t, "forget", "--session", "deja-note-claude-s1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "removed 1 promoted note") {
		t.Errorf("the removal is not reported:\n%s", out)
	}
	body, err = os.ReadFile(sources.NotesFile())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "240k") {
		t.Errorf("the forgotten note still holds its text:\n%s", body)
	}
	if !strings.Contains(string(body), "brake pads replaced") {
		t.Errorf("an unrelated note was removed too:\n%s", body)
	}

	// A note swept up by --project is a decision the reader deliberately kept:
	// #690 keeps it, and naming a session is the only case that may destroy it.
	out, err = captureRun(t, "forget", "--project", "proj")
	if err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(sources.NotesFile())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "brake pads replaced") {
		t.Errorf("--project destroyed a decision it should keep:\n%s\n%s", out, body)
	}
}
