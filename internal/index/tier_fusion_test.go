package index

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// fusionIndex builds a store big enough for the relevance tail to engage — it
// declines to rank a store no larger than its own window — holding one session
// that satisfies the strict AND incidentally and one that actually answers.
func fusionIndex(t *testing.T) string {
	t.Helper()
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	var sessions []model.Session
	for i := 0; i < 80; i++ {
		sessions = append(sessions, model.Session{
			ID: fmt.Sprintf("filler%02d", i), Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user", Text: "notes about the weekly planning meeting"}},
		})
	}
	// Carries every word of the question and answers none of it.
	sessions = append(sessions, model.Session{
		ID: "incidental", Harness: "claude", Project: "p", Updated: time.Now(),
		Messages: []model.Message{{Role: "user", Text: "many people own a bicycle; how many own a car is another question"}},
	})
	// Answers it, and is missing two of the question's words.
	sessions = append(sessions, model.Session{
		ID: "answer", Harness: "claude", Project: "p", Updated: time.Now(),
		Messages: []model.Message{{Role: "user", Text: "I keep three bicycles in the garage, the blue one is my commuter"}},
	})
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A thin strict result used to be concatenated in front of the whole ranked
// tail, so one session that happened to carry every query word led the answer
// however the ranking scored it — and a thin AND on a large store is exactly
// the case this tail exists for, so that head is usually incidental.
//
// The lead is bounded now: worth a fixed number of places rather than all of
// them. Pinned on the rule rather than on a staged result, because a strict
// match that the ranking puts more than ten places down is not something a
// synthetic corpus produces on demand — satisfying the AND is why they rank
// high in the first place. What that rule is worth in practice is day0bench:
// hit@5 28/40 -> 30/40, MRR .618 -> .622, longmemeval unmoved.
func TestStrictEvidenceIsWorthPlacesNotEverything(t *testing.T) {
	// Ranked far below something better: it gives way.
	if got, want := fusedPlace(30, true), fusedPlace(15, false); got <= want {
		t.Errorf("a strict match ranked 30th places at %d, ahead of a relevance match ranked 15th at %d", got, want)
	}
	// Ranked close behind: the AND is real evidence and still wins.
	if got, want := fusedPlace(5, true), fusedPlace(0, false); got >= want {
		t.Errorf("a strict match ranked 5th places at %d, behind a relevance match ranked first at %d", got, want)
	}
	// Two promoted sessions keep the order the ranking gave them. Clamping this
	// at zero collapsed them together and cost day0bench four questions at
	// rank 1.
	if fusedPlace(2, true) >= fusedPlace(9, true) {
		t.Error("promotion lost the ranking among the sessions it promoted")
	}
}

// And the evidence a strict match carries is still worth something definite:
// with nothing better ranked above it, it leads.
func TestAStrictMatchStillLeadsWhenNothingOutranksIt(t *testing.T) {
	dir := fusionIndex(t)
	result, err := SearchDetailed(dir, search.Options{Query: "bicycle car question", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) == 0 {
		t.Fatal("no results at all")
	}
	if got := result.Sessions[0].ID; got != "incidental" {
		t.Errorf("the only session holding every word of this query ranked behind %q", got)
	}
}
