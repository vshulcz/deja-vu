package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Asking for a forgotten session by the id you remember answered with the note
// promoted from it and said nothing, so the reply looked like the session
// itself until you noticed the id had changed (#971).
func TestAskingForAForgottenSessionSaysSo(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"the decision: keep the ticker window at 30s"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"s14","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s14.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "promote", "s14"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "forget", "--session", "s14"); err != nil {
		t.Fatal(err)
	}

	for _, cmd := range []string{"show", "ctx", "share"} {
		note, err := captureRunStderr(t, cmd, "s14")
		if err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
		if !strings.Contains(note, "is forgotten") {
			t.Errorf("%s answered with the note and did not say the session is gone: %q", cmd, note)
		}
	}

	// Asking for the note itself is not a surprise, and an ordinary query that
	// lands on it is not asking about the source at all.
	if note, err := captureRunStderr(t, "show", "deja-note-claude-s14"); err != nil {
		t.Fatal(err)
	} else if strings.Contains(note, "is forgotten") {
		t.Errorf("asking for the note by its own id was answered with a warning: %q", note)
	}
	if note, err := captureRunStderr(t, "ctx", "ticker window"); err != nil {
		t.Fatal(err)
	} else if strings.Contains(note, "is forgotten") {
		t.Errorf("an ordinary query was answered with a warning about a session nobody named: %q", note)
	}
}
