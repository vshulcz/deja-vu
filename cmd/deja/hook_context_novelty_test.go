package main

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// What the ordering does when the project has more to say than one digest can
// carry. On the store this was found on it changes nothing, and that is the
// finding rather than a failure: session start serves three sessions and only
// three qualified, so the 87.5% repeat rate there is the eligible set, not
// waste (#2038). Where the pool is larger, the agent hears something new.
func TestUnseenSessionsLeadWhenThereAreEnoughOfThem(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	const project = "wide"
	ss := []model.Session{
		{ID: "told-1"}, {ID: "told-2"}, {ID: "told-3"},
		{ID: "fresh-1"}, {ID: "fresh-2"},
	}
	rememberInjectedIDsFor(dir, "agent-one", sessionStartKeyPrefix+project, []string{"told-1", "told-2", "told-3"})

	got := leadWithUnseen(dir, []string{project}, ss)
	if len(got) != len(ss) {
		t.Fatalf("the ordering dropped candidates: %d in, %d out", len(ss), len(got))
	}
	if got[0].ID != "fresh-1" || got[1].ID != "fresh-2" {
		t.Errorf("what this project has not been told does not lead: %s, %s", got[0].ID, got[1].ID)
	}
	// Nothing is discarded: a start with no memory is worse than a repeat.
	seen := map[string]bool{}
	for _, s := range got {
		seen[s.ID] = true
	}
	for _, want := range []string{"told-1", "told-2", "told-3"} {
		if !seen[want] {
			t.Errorf("%s was dropped rather than demoted", want)
		}
	}
}

// With nothing new to say the order is left exactly as the ranking chose it —
// the case the real store is in, where reordering three sessions among
// themselves would only churn.
func TestNothingNewLeavesTheOrderAlone(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	const project = "narrow"
	ss := []model.Session{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	rememberInjectedIDsFor(dir, "agent-one", sessionStartKeyPrefix+project, []string{"a", "b", "c"})

	got := leadWithUnseen(dir, []string{project}, ss)
	for i := range ss {
		if got[i].ID != ss[i].ID {
			t.Fatalf("the order changed with nothing new to promote: %v", got)
		}
	}
}

// Among repeats, the one agents have asked for goes first: being pushed is not
// evidence, being pulled is — which is why the worn counter is built from
// agent-initiated recalls only.
func TestRepeatsAreOrderedByDemand(t *testing.T) {
	ss := []model.Session{{ID: "never-asked"}, {ID: "asked-twice"}, {ID: "asked-once"}}
	stableSortByDemand(ss, map[string]int{"asked-twice": 2, "asked-once": 1})
	if ss[0].ID != "asked-twice" || ss[1].ID != "asked-once" {
		t.Errorf("demand did not order the repeats: %v", ss)
	}
}
