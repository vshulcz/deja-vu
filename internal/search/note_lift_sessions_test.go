package search

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func sessionIDs(ss []model.Session) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.ID)
	}
	return out
}

// The per-prompt hook ranks sessions and never builds a Hit, so the rule the
// sort carries could not reach it: the transcript a note was distilled from
// took a slot in the block while the note itself waited behind it (#2803).
func TestNoteSessionsAreLiftedAboveTheirSource(t *testing.T) {
	ss := []model.Session{
		{ID: "other", Harness: "claude"},
		{ID: "sess1", Harness: "claude"},
		{ID: "unrelated", Harness: "claude"},
		{ID: "deja-note-claude-sess1", Harness: "deja"},
	}
	LiftNoteSessionsAboveTheirSource(ss)
	want := []string{"other", "deja-note-claude-sess1", "sess1", "unrelated"}
	got := sessionIDs(ss)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("note not lifted in front of its source: got %v want %v", got, want)
		}
	}
}

// A note already ahead of its source stays put, a note whose source is absent
// is left alone, and a harness whose name carries a dash does not split wrong.
func TestNoteSessionLiftLeavesEverythingElseAlone(t *testing.T) {
	ss := []model.Session{
		{ID: "deja-note-claude-sess1", Harness: "deja"},
		{ID: "sess1", Harness: "claude"},
		{ID: "deja-note-claude-gone", Harness: "deja"},
		{ID: "deja-2026-01-02-proj", Harness: "deja"},
	}
	before := sessionIDs(ss)
	LiftNoteSessionsAboveTheirSource(ss)
	after := sessionIDs(ss)
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("order changed with nothing to lift: %v -> %v", before, after)
		}
	}

	dashed := []model.Session{
		{ID: "one", Harness: "codex-history"},
		{ID: "deja-note-codex-history-one", Harness: "deja"},
	}
	LiftNoteSessionsAboveTheirSource(dashed)
	if got := sessionIDs(dashed); got[0] != "deja-note-codex-history-one" {
		t.Errorf("a harness name with a dash was not matched: %v", got)
	}

	LiftNoteSessionsAboveTheirSource(nil)
}
