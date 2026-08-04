package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The notes file is a store file, and it was counted by existence: an empty one
// — a note written and later forgotten — made deja answer "run `deja index`"
// one line under the index run it had just narrated (#996).
func TestAnEmptyNotesFileIsNotAgentHistory(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	if err := os.WriteFile(notes, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !noAgentHistoryFound() {
		t.Error("an empty notes file was counted as history")
	}
	if err := os.WriteFile(notes, []byte(`{"ts":"2026-08-04T10:00:00Z","project":"p","text":"the ticker window stays at 30s"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if noAgentHistoryFound() {
		t.Error("a note someone wrote stopped counting as history")
	}
	if hint := emptyIndexHint("no matches"); !strings.Contains(hint, "deja index") {
		t.Errorf("a machine with history lost the advice that fits it: %q", hint)
	}
}
