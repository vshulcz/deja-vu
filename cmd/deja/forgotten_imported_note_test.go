package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
)

// The line that says which forgotten session a note came from is matched on the
// note's local id, and a note that arrived by sync carries `imported-…` with
// the real id in OrigID — so the reader who asks about that session by name is
// handed the note with the fact removed (#2839, the family #2833 closed for the
// ranking).
func TestAnImportedNoteStillNamesItsForgottenSource(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(tmp, "claude", "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"longs","cwd":"/w/p","timestamp":"2026-06-01T10:00:00Z",` +
		`"message":{"role":"user","content":"the goblin pool deadlocks"}}`
	if err := os.WriteFile(filepath.Join(store, "longs.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "forget", "--session", "longs"); err != nil {
		t.Fatal(err)
	}

	note := model.Session{
		Harness: "deja", ID: "imported-ccc333", OrigID: "deja-note-claude-longs",
		Project: "imported:api",
	}
	if got := forgottenSourceNote(index.DefaultDir(), note, "claude:longs", false); !strings.Contains(got, "forgotten") {
		t.Errorf("an imported note says nothing about the session it distils: %q", got)
	}

	local := model.Session{Harness: "deja", ID: "deja-note-claude-longs", Project: "api"}
	if got := forgottenSourceNote(index.DefaultDir(), local, "claude:longs", false); !strings.Contains(got, "forgotten") {
		t.Errorf("the local case regressed: %q", got)
	}
}
