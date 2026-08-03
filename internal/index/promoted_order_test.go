package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// Promoted notes are served newest-first so the latest correction is the answer
// that holds (#812). An incremental build appends the new lines to what the log
// already has, so the order came out oldest-first and the correction sat last —
// the failure #812 was written for (#944).
func TestPromotedNoteKeepsCorrectionsNewestFirstAfterAnIncrementalBuild(t *testing.T) {
	tmp := t.TempDir()
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))

	line := func(ts, state, text string) string {
		b, err := json.Marshal(map[string]string{
			"ts": ts, "project": "p", "text": text,
			"kind": "promoted", "session": "claude:s1", "state": state, "title": "pool cap",
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	body := line("2026-07-12T10:00:00Z", "accepted", "first answer: raise the pool cap to 200") +
		line("2026-07-13T10:00:00Z", "accepted", "second answer: 50 is enough")
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// The correction arrives after the index exists, so the next build is
	// incremental.
	if err := os.WriteFile(notes, []byte(body+line("2026-07-20T10:00:00Z", "rejected", "rolled back, the cap stays at 20")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	s, ok, err := FindByPrefix(dir, "deja-note-claude-s1")
	if err != nil || !ok {
		t.Fatalf("promoted note not found: %v", err)
	}
	if len(s.Messages) != 3 {
		t.Fatalf("got %d messages, want 3", len(s.Messages))
	}
	if !strings.Contains(s.Messages[0].Text, "rolled back") {
		t.Errorf("the note leads with %q, not the newest correction", s.Messages[0].Text)
	}

	// The search path assembles sessions separately and must agree.
	ss, err := Search(dir, query.Options{Query: "cap", All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range ss {
		if got.ID != "deja-note-claude-s1" || len(got.Messages) < 2 {
			continue
		}
		if !strings.Contains(got.Messages[0].Text, "rolled back") {
			t.Errorf("search leads with %q, not the newest correction", got.Messages[0].Text)
		}
	}
}
