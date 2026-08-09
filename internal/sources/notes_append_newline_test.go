package sources

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// notes.jsonl is documented as a hand-editable file. An editor that drops the
// final newline leaves the last record without one; the next `deja note` then
// appended its JSON glued onto that line, and the reader — which decodes only
// the first value per line — silently dropped the new note. The append must
// start a fresh line first.
func TestAppendNoteStartsAFreshLineWhenFileLacksTrailingNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)

	// A pre-existing record with NO trailing newline, as a hand-edit can leave.
	first := `{"ts":"2026-01-01T00:00:00Z","project":"proj","text":"first note"}`
	if err := os.WriteFile(path, []byte(first), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := AppendNote("proj", "second note", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	got, err := ParseNotesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, s := range got {
		for _, m := range s.Messages {
			texts = append(texts, m.Text)
		}
	}
	joined := ""
	for _, x := range texts {
		joined += x + "|"
	}
	if !contains(texts, "first note") {
		t.Errorf("first note lost: %q", joined)
	}
	if !contains(texts, "second note") {
		t.Errorf("second note lost — glued onto the previous line: %q", joined)
	}
}
