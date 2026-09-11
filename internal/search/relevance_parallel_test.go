package search

import (
	"fmt"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Hits are built on many cores now — lowercasing every message of every ranked
// session was the other half of a 2.4 s recall answer. The page an agent reads
// has to be the same one either way, snippets included.
func TestRelevanceHitsAreTheSameOnOneCoreAndMany(t *testing.T) {
	var ss []model.Session
	for i := range 20 {
		ss = append(ss, model.Session{
			Harness: "claude", ID: fmt.Sprintf("s%02d", i),
			Messages: []model.Message{
				{Role: "user", Text: fmt.Sprintf("what did we settle about the retry budget in round %d", i)},
				{Role: "assistant", Text: fmt.Sprintf("capped at %d, and the scheduler stayed single-writer", i%4)},
			},
		})
	}
	terms := []string{"retry", "budget", "scheduler"}
	idf := map[string]float64{"retry": 1.5, "budget": 2, "scheduler": 3}

	relevanceWorkers = func() int { return 1 }
	one := RelevanceHitsWeighted(ss, terms, idf)
	relevanceWorkers = func() int { return 8 }
	many := RelevanceHitsWeighted(ss, terms, idf)
	t.Cleanup(func() { relevanceWorkers = defaultRelevanceWorkers })

	if len(one) != len(many) {
		t.Fatalf("one core returned %d hits, eight returned %d", len(one), len(many))
	}
	for i := range one {
		if one[i].Session.ID != many[i].Session.ID {
			t.Fatalf("position %d: one core says %s, eight say %s", i, one[i].Session.ID, many[i].Session.ID)
		}
		if one[i].Count != many[i].Count || one[i].Score != many[i].Score {
			t.Fatalf("%s scored %v/%d on one core and %v/%d on eight",
				one[i].Session.ID, one[i].Score, one[i].Count, many[i].Score, many[i].Count)
		}
		if len(one[i].Snippets) != len(many[i].Snippets) {
			t.Fatalf("%s carries %d snippets on one core and %d on eight",
				one[i].Session.ID, len(one[i].Snippets), len(many[i].Snippets))
		}
		for j := range one[i].Snippets {
			if one[i].Snippets[j] != many[i].Snippets[j] {
				t.Fatalf("%s snippet %d differs between the two paths", one[i].Session.ID, j)
			}
		}
	}
}
