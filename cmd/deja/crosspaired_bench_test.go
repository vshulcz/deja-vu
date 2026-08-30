package main

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/bench"
)

// The arm is only worth reading if no question is ever asked where its answer
// lives. Three chains of one project sit next to each other in the corpus, so
// stepping once lands back at home — measured, that counted five sessions
// holding the answer as false fires, of eleven.
func TestCrossPairingNeverAsksAtHome(t *testing.T) {
	crossed := []PromptChainRef{
		{"a", "one", "a"},
		{"b", "one", "b"},
		{"c", "one", "c"},
		{"d", "two", "d"},
	}
	for i, c := range crossed {
		away, ok := awayFor(crossed, i)
		if !ok {
			t.Errorf("%q found nowhere to be asked", c.ID)
			continue
		}
		if away.Project == c.Project {
			t.Errorf("%q was asked in its own project %q", c.ID, c.Project)
		}
	}
}

// The haystack holds one session that mentions every subject in the corpus, and
// the bucket is the catch-all scope. Asking either about anything is not asking
// somewhere the answer cannot be.
func TestCrossPairingSkipsTheProjectsThatHoldEverything(t *testing.T) {
	crossed := []PromptChainRef{
		{"a", "one", "a"},
		{"b", bench.PromptHaystackProject, "b"},
		{"c", bench.PromptBucketProject, "c"},
		{"d", "two", "d"},
	}
	away, ok := awayFor(crossed, 0)
	if !ok {
		t.Fatal("nowhere to ask")
	}
	if away.Project != "two" {
		t.Errorf("asked in %q, which holds every subject by construction", away.Project)
	}
}
