package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A promoted note's display line has to carry the decision, not the question
// the session opened with. `promote` borrows that opening line as the title,
// so `deja last` rendered "should the retry budget go up to 10? [accepted]"
// for a note whose text says the budget stays at 5 — the inverse of what was
// decided, the shape #2456 fixed for the standing-decisions line (#2539).
func TestPromotedNoteTitleLeadsWithTheDecision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	lines := []string{
		`{"ts":"2026-07-21T10:00:00Z","project":"p","text":"the retry budget stays at 5; the pool change is what fixed the timeouts","kind":"promoted","session":"claude:dec","state":"accepted","title":"should the retry budget go up to 10 for the payments client?"}`,
		`{"ts":"2026-07-21T10:00:00Z","project":"p","text":"keep the sidebar collapsed by default","kind":"promoted","session":"claude:side","state":"rejected"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions, err := ParseNotesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]string{}
	for _, s := range sessions {
		titles[s.ID] = s.Title
	}
	got := titles[PromotedNoteID("claude:dec")]
	if !strings.HasPrefix(got, "the retry budget stays at 5") {
		t.Errorf("title leads with %q, want the decision", got)
	}
	if strings.Contains(got, "go up to 10") {
		t.Errorf("the question the note answered is still the display line: %q", got)
	}
	// The state stays where every one-line surface reads it, at the tail
	// SafeNoteTitle carries past its clip (#R11).
	if !strings.HasSuffix(got, " [accepted]") {
		t.Errorf("title lost its state: %q", got)
	}
	if got := titles[PromotedNoteID("claude:side")]; got != "keep the sidebar collapsed by default [rejected]" {
		t.Errorf("note with no borrowed title: %q", got)
	}
}
