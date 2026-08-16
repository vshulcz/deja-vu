package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Estimating a term's worth from the answer set has one blind spot, and it is
// not a corner: the answer set was selected for the rare term, so inside it the
// rare term is everywhere. "How many bikes do I own" returns sessions about
// bikes — every one of them holds `bikes` and one of them happens to hold
// `many`, so by the set's own arithmetic `many` is the rarer word and the
// excerpt centres on it.
//
// The ranking already knows what the words are worth against the whole corpus.
// Handing that down is what this checks.
func TestTheRankingsOwnWeightsBeatEstimatingThemFromTheAnswerSet(t *testing.T) {
	var ss []model.Session
	for i := 0; i < 4; i++ {
		ss = append(ss, model.Session{
			ID: string(rune('a' + i)), Harness: "claude", Project: "p",
			Messages: []model.Message{{Role: "user", Text: "a review of touring bikes for long rides"}},
		})
	}
	ss = append(ss, model.Session{
		ID: "answer", Harness: "claude", Project: "p",
		Messages: []model.Message{
			{Role: "user", Text: "how many of these are there, generally speaking"},
			{Role: "user", Text: "I keep three bikes in the garage"},
		},
	})
	terms := []string{"many", "bikes"}

	// What the corpus says: bikes is rare in nineteen hundred sessions, many is
	// not. Within these five, the arithmetic is the other way round.
	corpus := map[string]float64{"many": 0.4, "bikes": 4.5}

	estimated := snippetFor(t, RelevanceHits(ss, terms), "answer")
	exact := snippetFor(t, RelevanceHitsWeighted(ss, terms, corpus), "answer")

	if strings.Contains(strings.ToLower(estimated), "bikes") {
		t.Skip("the estimate happened to agree here; this test is about the case where it cannot")
	}
	if !strings.Contains(strings.ToLower(exact), "bikes") {
		t.Errorf("with the ranking's own weights the excerpt is still the filler message:\n  %s", exact)
	}
}

func snippetFor(t *testing.T, hits []Hit, id string) string {
	t.Helper()
	for _, h := range hits {
		if h.Session.ID == id {
			if len(h.Snippets) == 0 {
				t.Fatalf("no snippet for %q", id)
			}
			return h.Snippets[0]
		}
	}
	t.Fatalf("%q is not in the hits", id)
	return ""
}
