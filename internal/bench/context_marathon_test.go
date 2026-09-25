package bench

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The fold is the whole point of the corpus: one long session where there were
// fourteen, with the task session left where it was.
func TestFoldChainKeepsTheTaskApart(t *testing.T) {
	chain := ContextChain{
		ID: "c", Sessions: []model.Session{
			{ID: "a", Harness: "claude", Project: "p", Messages: []model.Message{{Text: "one"}}},
			{ID: "b", Harness: "claude", Project: "p", Messages: []model.Message{{Text: "two"}, {Text: "three"}}},
			{ID: "c-task", Harness: "claude", Project: "p", Messages: []model.Message{{Text: "the task"}}},
		},
	}
	got := foldChain(chain)
	if len(got.Sessions) != 2 {
		t.Fatalf("folded into %d sessions", len(got.Sessions))
	}
	if n := len(got.Sessions[0].Messages); n != 3 {
		t.Errorf("the folded session holds %d messages, the priors had 3", n)
	}
	if got.Sessions[1].ID != "c-task" {
		t.Errorf("the task session moved: %q", got.Sessions[1].ID)
	}
	if got.Sessions[0].ID != "c-marathon" {
		t.Errorf("the folded session is named %q", got.Sessions[0].ID)
	}
}

// A chain with nothing to fold, and one with only its task: neither is a
// shape the generator produces, and both used to be the only untested way
// through the function.
func TestFoldChainWithNothingToFold(t *testing.T) {
	if got := foldChain(ContextChain{ID: "empty"}); len(got.Sessions) != 0 {
		t.Errorf("an empty chain folded into %d sessions", len(got.Sessions))
	}
	only := ContextChain{ID: "t", Sessions: []model.Session{{ID: "t-task"}}}
	got := foldChain(only)
	if len(got.Sessions) != 1 || got.Sessions[0].ID != "t-task" {
		t.Errorf("a chain of one session folded into %+v", got.Sessions)
	}
}

// The corpus takes the first ContextMarathonCount real chains and no negative
// controls: a control has no facts, so coverage over it means nothing.
func TestTheMarathonCorpusSkipsTheNegativeControls(t *testing.T) {
	corpus := GenerateContextMarathon(3)
	if len(corpus.Chains) != ContextMarathonCount {
		t.Fatalf("marathon chains: %d, want %d", len(corpus.Chains), ContextMarathonCount)
	}
	for _, c := range corpus.Chains {
		if c.Negative {
			t.Errorf("%s is a negative control", c.ID)
		}
		if len(c.Facts) == 0 {
			t.Errorf("%s carries no facts to score against", c.ID)
		}
	}
	if corpus.Hash == GenerateContext(3).Hash {
		t.Error("the folded corpus hashes the same as the corpus it came from")
	}
	if corpus.Hash == GenerateContextMarathon(4).Hash {
		t.Error("two seeds hash the same")
	}
}
