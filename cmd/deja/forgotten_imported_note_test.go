package main

import (
	"encoding/json"
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
	if got := forgottenSourceNote(note, "claude:longs", false); !strings.Contains(got, "forgotten") {
		t.Errorf("an imported note says nothing about the session it distils: %q", got)
	}

	local := model.Session{Harness: "deja", ID: "deja-note-claude-longs", Project: "api"}
	if got := forgottenSourceNote(local, "claude:longs", false); !strings.Contains(got, "forgotten") {
		t.Errorf("the local case regressed: %q", got)
	}
}

// And the case where the transcript travelled too: it is tombstoned under the
// id it has here, while the note names the id it had there. Asking about
// either has to say the session is gone (#2839).
func TestAnImportedSourceIsStillNamedAsForgotten(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	var batch []byte
	for _, r := range []index.SyncRecord{
		{Harness: "claude", SessionID: "longs", Project: "api", Role: "user", Text: "the goblin pool deadlocks"},
	} {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		batch = append(batch, append(b, '\n')...)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync.jsonl"), batch, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	metas, err := index.AllMeta(index.DefaultDir())
	if err != nil {
		t.Fatal(err)
	}
	local := ""
	for _, m := range metas {
		if m.OrigID == "longs" {
			local = m.ID
		}
	}
	if local == "" {
		t.Fatalf("the imported session is not in the index: %+v", metas)
	}
	if _, err := captureRunStderr(t, "forget", "--session", local); err != nil {
		t.Fatal(err)
	}

	note := model.Session{
		Harness: "deja", ID: "imported-ccc333", OrigID: "deja-note-claude-longs",
		Project: "imported:api",
	}
	got := forgottenSourceNote(note, "claude:longs", false)
	if !strings.Contains(got, "forgotten") {
		t.Errorf("a forgotten imported source is not named: %q", got)
	}
	// And it names the key `deja forget --list` will show, which is the local
	// one rather than the id the note carries.
	if !strings.Contains(got, local) {
		t.Errorf("the line names a key the reader cannot find: %q, local key holds %q", got, local)
	}
}
