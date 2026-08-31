package search

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
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
	// Source first, which is the order a score-only ranking produces when the
	// transcript is the longer text.
	ss := []model.Session{source, note}

	for name, hits := range map[string][]Hit{
		"error":     ErrorHits(ss),
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
	}
}
