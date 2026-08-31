package search

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The note-over-source rule lived in the sort, so it reached the tiers that go
// through it and missed the two that arrive already ranked: a pasted error and
// the "ranked by relevance" screen served a note behind the transcript it was
// distilled from — the one ordering `promote` prints a promise about (#2803).
func TestTheErrorAndRelevanceTiersLiftANoteOverItsSource(t *testing.T) {
	note := model.Session{ID: "deja-note-claude-sess1", Harness: "deja",
		Messages: []model.Message{{Role: "user", Text: "pgbouncer pool cap settled at 40"}}}
	source := model.Session{ID: "sess1", Harness: "claude",
		Messages: []model.Message{{Role: "user", Text: "pgbouncer pool cap settled at 40 after a long argument"}}}
	other := model.Session{ID: "other", Harness: "claude",
		Messages: []model.Message{{Role: "user", Text: "pgbouncer restarts on deploy"}}}

	// Ranked with the source ahead of its own note, which is the order the
	// callers of these two builders hand in.
	ranked := []model.Session{other, source, note}

	for _, tc := range []struct {
		name string
		hits []Hit
	}{
		{"error", ErrorHits(ranked)},
		{"relevance", RelevanceHitsWeighted(ranked, []string{"pgbouncer", "pool"}, nil)},
	} {
		var at = map[string]int{}
		for i, h := range tc.hits {
			at[h.Session.ID] = i
		}
		if at["deja-note-claude-sess1"] > at["sess1"] {
			t.Errorf("%s: the note is behind the transcript it was promoted from: %v", tc.name, liftIDs(tc.hits))
		}
		// The scores have to agree with the order, or a caller that re-sorts
		// reads the old one back.
		for i := 1; i < len(tc.hits); i++ {
			if tc.hits[i-1].Score < tc.hits[i].Score {
				t.Errorf("%s: score disagrees with position at %d: %v", tc.name, i, liftIDs(tc.hits))
			}
		}
		// And nothing else moved: the rule reorders one pair.
		if at["other"] != 0 {
			t.Errorf("%s: an unrelated session was moved: %v", tc.name, liftIDs(tc.hits))
		}
	}
}
