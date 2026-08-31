package search

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A note that arrived by sync has its local id rewritten to imported-… and
// keeps the real one in OrigID. The lift recognised a note by its id alone, so
// an imported note was never recognised and the transcript it distils outranked
// it everywhere the rule reaches (#2833).
func TestAnImportedNoteIsLiftedAboveItsSource(t *testing.T) {
	hits := []Hit{
		{Session: model.Session{ID: "other", Harness: "claude"}},
		{Session: model.Session{ID: "sess1", Harness: "claude"}},
		{Session: model.Session{ID: "imported-abc", OrigID: "deja-note-claude-sess1", Harness: "deja"}},
	}
	liftNotesAboveTheirSource(hits)
	if got := liftIDs(hits); got[1] != "imported-abc" {
		t.Errorf("an imported note was not lifted in front of its source: %v", got)
	}
}

// And the other side of the pair: an imported *source* is keyed by its
// original id too, so a note about it has to be matched on that.
func TestANoteIsLiftedAboveAnImportedSource(t *testing.T) {
	hits := []Hit{
		{Session: model.Session{ID: "other", Harness: "claude"}},
		{Session: model.Session{ID: "imported-def", OrigID: "sess1", Harness: "claude"}},
		{Session: model.Session{ID: "deja-note-claude-sess1", Harness: "deja"}},
	}
	liftNotesAboveTheirSource(hits)
	if got := liftIDs(hits); got[1] != "deja-note-claude-sess1" {
		t.Errorf("a note about an imported source was not lifted: %v", got)
	}
}

// Both at once, which is what a synced pair actually looks like.
func TestAnImportedNoteIsLiftedAboveAnImportedSource(t *testing.T) {
	hits := []Hit{
		{Session: model.Session{ID: "imported-def", OrigID: "sess1", Harness: "claude"}},
		{Session: model.Session{ID: "imported-abc", OrigID: "deja-note-claude-sess1", Harness: "deja"}},
	}
	liftNotesAboveTheirSource(hits)
	if got := liftIDs(hits); got[0] != "imported-abc" {
		t.Errorf("a synced pair was left in source-first order: %v", got)
	}
}

// The same rule for a caller that ranks sessions rather than hits.
func TestAnImportedNoteSessionIsLiftedAboveItsSource(t *testing.T) {
	ss := []model.Session{
		{ID: "sess1", Harness: "claude"},
		{ID: "imported-abc", OrigID: "deja-note-claude-sess1", Harness: "deja"},
	}
	LiftNoteSessionsAboveTheirSource(ss)
	if got := sessionIDs(ss); got[0] != "imported-abc" {
		t.Errorf("an imported note was not lifted on the session path: %v", got)
	}
}
