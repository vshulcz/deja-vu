package search

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A note that arrived by sync keeps its own id in OrigID and carries an
// `imported-…` one locally, so the rule that recognises a note by its id never
// saw it — and the transcript it distils outranked it on every surface (#2833).
// Both sides are rewritten: the source arrives imported too.
func TestAnImportedNoteIsLiftedAboveItsSource(t *testing.T) {
	now := time.Now()
	source := model.Session{
		Harness: "claude", ID: "imported-aaa111", OrigID: "longs", Project: "imported:api", Updated: now,
		Messages: []model.Message{{Role: "user", Text: "the goblin pool deadlocks under load", Time: now}},
	}
	note := model.Session{
		Harness: "deja", ID: "imported-bbb222", OrigID: "deja-note-claude-longs",
		Project: "imported:api", Updated: now,
		Messages: []model.Message{{Role: "user", Text: "[accepted] the goblin pool was too small", Time: now}},
	}
	hits := []Hit{{Session: source}, {Session: note}}
	LiftNotesAboveTheirSource(hits)
	if hits[0].Session.ID != note.ID {
		t.Errorf("the imported transcript stayed above the note made from it: %s first", hits[0].Session.ID)
	}
}

// And the half-imported case: the note came across, the transcript is this
// machine's own.
func TestAnImportedNoteIsLiftedAboveALocalSource(t *testing.T) {
	now := time.Now()
	source := model.Session{
		Harness: "claude", ID: "longs", Project: "api", Updated: now,
		Messages: []model.Message{{Role: "user", Text: "the goblin pool deadlocks under load", Time: now}},
	}
	note := model.Session{
		Harness: "deja", ID: "imported-bbb222", OrigID: "deja-note-claude-longs",
		Project: "imported:api", Updated: now,
		Messages: []model.Message{{Role: "user", Text: "[accepted] the goblin pool was too small", Time: now}},
	}
	hits := []Hit{{Session: source}, {Session: note}}
	LiftNotesAboveTheirSource(hits)
	if hits[0].Session.ID != note.ID {
		t.Errorf("an imported note lost to a local transcript: %s first", hits[0].Session.ID)
	}
}
