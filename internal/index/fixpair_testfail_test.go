package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A failing test is repaired by an edit, so the command that followed one is
// the session moving on — and the work moves on the same way every day, which
// used to be enough to call it a fix. `deja fix` answered eleven red tests on
// this machine with `gh pr merge`, `git checkout -b` and `git status`.
func TestACommandAfterARedTestIsNotConfirmedByRepetition(t *testing.T) {
	now := time.Now()
	session := func(id string) []FixPair {
		return fixPairsIn([]model.Message{
			{Role: "tool-output", Text: "--- FAIL: TestBillingRoundsHalfUp (0.09s)", Time: now},
			{Role: "command", Text: "gh pr merge 2532 --squash --delete-branch", Time: now.Add(time.Minute)},
		}, "claude:"+id, "p")
	}
	first := mergeFixPairs(nil, session("s1"))
	if len(first) != 1 || !first[0].Candidate {
		t.Fatalf("one sighting is a candidate: %+v", first)
	}
	second := mergeFixPairs(first, session("s2"))
	if len(second) != 1 {
		t.Fatalf("the sighting is kept, not dropped: %+v", second)
	}
	if !second[0].Candidate {
		t.Errorf("a second session running the same command after the same red test confirmed nothing: %+v", second[0])
	}
}

// The rule is about that shape only. `brew services start postgresql` names
// nothing `psql: connection refused` names either, and a second session running
// it is still the answer.
func TestARepeatedCommandAfterAnOrdinaryFailureStillConfirms(t *testing.T) {
	now := time.Now()
	session := func(id string) []FixPair {
		return fixPairsIn([]model.Message{
			{Role: "tool-output", Text: "psql: connection refused on port 5432", Time: now},
			{Role: "command", Text: "brew services start postgresql", Time: now.Add(time.Minute)},
		}, "claude:"+id, "p")
	}
	second := mergeFixPairs(mergeFixPairs(nil, session("s1")), session("s2"))
	if len(second) != 1 || second[0].Candidate {
		t.Fatalf("a repeated remedy for an ordinary failure must be a pair: %+v", second)
	}
}

// And an edit after a red test is exactly what a repair looks like, so the
// second sighting confirms it.
func TestAnEditAfterARedTestIsStillConfirmedByRepetition(t *testing.T) {
	now := time.Now()
	session := func(id string) []FixPair {
		return fixPairsIn([]model.Message{
			{Role: "tool-output", Text: "--- FAIL: TestBillingRoundsHalfUp (0.09s)", Time: now},
			{Role: "edit", Text: "internal/billing/round.go\n-\tmath.Round(v)\n+\tmath.Floor(v + 0.5)", Time: now.Add(time.Minute)},
		}, "claude:"+id, "p")
	}
	second := mergeFixPairs(mergeFixPairs(nil, session("s1")), session("s2"))
	if len(second) != 1 || second[0].Candidate {
		t.Fatalf("a repeated edit after a red test must be a pair: %+v", second)
	}
}
