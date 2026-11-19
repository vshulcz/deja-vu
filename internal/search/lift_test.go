package search

import (
	"github.com/vshulcz/deja-vu/internal/model"
	"testing"
	"time"
)

// `deja promote` tells the reader the note now outranks the transcript it was
// made from. Two tiers build their hits themselves and never called the lift,
// so on a pasted error and on the "ranked by relevance" screen the transcript
// came back above its own note (#2803).
func TestEveryTierPutsANoteAboveItsSource(t *testing.T) {
	now := time.Now()
	source := model.Session{
		Harness: "claude", ID: "longs", Project: "api", Updated: now,
		Messages: []model.Message{{Role: "user", Text: "the goblin pool deadlocks under load", Time: now}},
	}
	note := model.Session{
		Harness: "deja", ID: "deja-note-claude-longs", Project: "api", Updated: now,
		Messages: []model.Message{{Role: "user", Text: "[accepted] the goblin pool was too small", Time: now}},
	}
	// A session between them, so the lift has to rotate rather than swap: a
	// two-element fixture cannot tell the two apart.
	between := model.Session{
		Harness: "claude", ID: "other", Project: "api", Updated: now,
		Messages: []model.Message{{Role: "user", Text: "goblin pool notes from an unrelated session", Time: now}},
	}
	// Source first, which is the order a score-only ranking produces when the
	// transcript is the longer text.
	ss := []model.Session{source, between, note}

	for name, hits := range map[string][]Hit{
		"error":     ErrorHits(ss, ""),
		"relevance": RelevanceHits(ss, []string{"goblin", "pool"}),
	} {
		var noteAt, sourceAt = -1, -1
		for i, h := range hits {
			switch h.Session.ID {
			case "deja-note-claude-longs":
				noteAt = i
			case "longs":
				sourceAt = i
			}
		}
		if noteAt < 0 || sourceAt < 0 {
			t.Fatalf("%s tier: both should be in the answer, got %d hits", name, len(hits))
		}
		if noteAt > sourceAt {
			t.Errorf("%s tier put the transcript above its own note", name)
		}
		// And the hit in between kept its place relative to the source rather
		// than being swapped past it.
		for i, h := range hits {
			if h.Session.ID == "other" && i < sourceAt {
				t.Errorf("%s tier moved an unrelated hit above the source", name)
			}
		}
	}
}

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

// The other direction: a transcript that arrived by sync and was promoted
// *here*. promote builds the note id from the local id, so the note names
// `imported-…` — and a rule that only looked through OrigID would miss it,
// which is the pairing the first fix traded away.
func TestANoteMadeHereAboutAnImportedSourceIsLifted(t *testing.T) {
	now := time.Now()
	source := model.Session{
		Harness: "claude", ID: "imported-aaa111", OrigID: "longs", Project: "imported:api", Updated: now,
		Messages: []model.Message{{Role: "user", Text: "the goblin pool deadlocks under load", Time: now}},
	}
	note := model.Session{
		Harness: "deja", ID: "deja-note-claude-imported-aaa111", Project: "api", Updated: now,
		Messages: []model.Message{{Role: "user", Text: "[accepted] the goblin pool was too small", Time: now}},
	}
	other := model.Session{
		Harness: "claude", ID: "unrelated", Project: "api", Updated: now,
		Messages: []model.Message{{Role: "user", Text: "something else entirely", Time: now}},
	}
	hits := []Hit{{Session: source}, {Session: other}, {Session: note}}
	LiftNotesAboveTheirSource(hits)
	if hits[0].Session.ID != note.ID {
		t.Errorf("the note made here about an imported transcript did not lift: %s first", hits[0].Session.ID)
	}
	// A rotation, not a swap: the unrelated hit keeps its place behind the
	// source rather than jumping over it.
	if hits[1].Session.ID != source.ID || hits[2].Session.ID != other.ID {
		t.Errorf("the hits between the pair were not rotated: %s, %s", hits[1].Session.ID, hits[2].Session.ID)
	}
}

// Two machines promoted the same session, and one of the notes travelled. Both
// end above the source, and this machine's own note is the one on top: the tie
// deja breaks everywhere else.
func TestThisMachinesOwnNoteOutranksAnImportedCopy(t *testing.T) {
	now := time.Now()
	source := model.Session{
		Harness: "claude", ID: "longs", Project: "api", Updated: now,
		Messages: []model.Message{{Role: "user", Text: "the goblin pool deadlocks under load", Time: now}},
	}
	local := model.Session{
		Harness: "deja", ID: "deja-note-claude-longs", Project: "api", Updated: now,
		Messages: []model.Message{{Role: "user", Text: "[accepted] the goblin pool was too small", Time: now}},
	}
	peer := model.Session{
		Harness: "deja", ID: "imported-ccc333", OrigID: "deja-note-claude-longs",
		Project: "imported:api", Updated: now,
		Messages: []model.Message{{Role: "user", Text: "[accepted] the pool was too small", Time: now}},
	}
	hits := []Hit{{Session: source}, {Session: local}, {Session: peer}}
	LiftNotesAboveTheirSource(hits)
	if hits[0].Session.ID != local.ID {
		t.Errorf("the peer's copy was put above this machine's own note: %s first", hits[0].Session.ID)
	}
	for i, h := range hits {
		if h.Session.ID == source.ID && i == 0 {
			t.Error("the source stayed on top")
		}
	}
}
