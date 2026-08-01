package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Both shapes were written by deja itself in earlier versions, and both
// vanished at index time with nothing reported — the user's own decisions are
// the one class of content deja cannot re-derive (#771).
func TestParseNotesKeepsOlderShapes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	lines := []string{
		`{"ts":"2026-07-20T10:00:00Z","project":"proj/p","text":"an old plain note"}`,
		`{"ts":"2026-07-21T10:00:00Z","project":"proj/p","text":"a promoted note with no state","kind":"promoted","session":"claude:t1"}`,
		`{"ts":"2026-07-22T10:00:00Z","text":"a note with no project at all"}`,
		// Still rejected: a promoted note with no source has nothing to attach
		// to, and an empty body is not a note.
		`{"ts":"2026-07-23T10:00:00Z","project":"p","text":"orphan","kind":"promoted"}`,
		`{"ts":"2026-07-24T10:00:00Z","project":"p","text":"   "}`,
		`{"ts":"not a date","project":"p","text":"unparseable stamp"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseNotesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, s := range ss {
		for _, m := range s.Messages {
			texts = append(texts, m.Text)
		}
	}
	joined := strings.Join(texts, "\n")
	for _, want := range []string{"an old plain note", "a promoted note with no state", "a note with no project at all"} {
		if !strings.Contains(joined, want) {
			t.Errorf("dropped %q:\n%s", want, joined)
		}
	}
	for _, unwanted := range []string{"orphan", "unparseable stamp"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("kept %q, which has nothing to attach to", unwanted)
		}
	}
	// Dropping it is right; dropping it invisibly is not. A promoted line with
	// no source used to leave no trace at all — not in the index, not in the
	// skipped count (#814).
	malformed, _ := DiagSnapshot()
	if malformed[path] == 0 {
		t.Errorf("a promoted note with no source was dropped without being counted")
	}
	// A note with no project is filed under one rather than losing its home.
	var found bool
	for _, s := range ss {
		if s.Project == "notes" {
			found = true
		}
	}
	if !found {
		t.Errorf("the project-less note has no project: %#v", ss)
	}
}
