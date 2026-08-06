package search

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func liftIDs(hits []Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Session.ID)
	}
	return out
}

// A promoted note carries one distilled line; the transcript it came from
// carries the query words in its title and repeats them throughout. On score
// alone the transcript wins its own distillation, which is the opposite of
// what `promote` tells the user it just did.
func TestPromotedNoteIsLiftedAboveItsSourceTranscript(t *testing.T) {
	hits := []Hit{
		{Session: model.Session{ID: "other", Harness: "claude"}, Score: 9},
		{Session: model.Session{ID: "sess1", Harness: "claude"}, Score: 5},
		{Session: model.Session{ID: "unrelated", Harness: "claude"}, Score: 3},
		{Session: model.Session{ID: "deja-note-claude-sess1", Harness: "deja"}, Score: 1},
	}
	liftNotesAboveTheirSource(hits)
	got := liftIDs(hits)
	want := []string{"other", "deja-note-claude-sess1", "sess1", "unrelated"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("note not lifted in front of its source: got %v want %v", got, want)
		}
	}
}

// A note already ahead of its source stays put, and a note whose source is not
// in the results is left alone — the rule reorders one pair, nothing else.
func TestNoteLiftLeavesUnrelatedOrderAlone(t *testing.T) {
	hits := []Hit{
		{Session: model.Session{ID: "deja-note-claude-sess1", Harness: "deja"}, Score: 9},
		{Session: model.Session{ID: "sess1", Harness: "claude"}, Score: 5},
		{Session: model.Session{ID: "deja-note-claude-gone", Harness: "deja"}, Score: 4},
		{Session: model.Session{ID: "deja-2026-01-02-proj", Harness: "deja"}, Score: 2},
	}
	before := liftIDs(hits)
	liftNotesAboveTheirSource(hits)
	after := liftIDs(hits)
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("order changed with nothing to lift: %v -> %v", before, after)
		}
	}

	none := []Hit{{Session: model.Session{ID: "sess1", Harness: "claude"}, Score: 1}}
	liftNotesAboveTheirSource(none)
	if none[0].Session.ID != "sess1" {
		t.Fatalf("no notes present, nothing to do: %v", liftIDs(none))
	}
}
