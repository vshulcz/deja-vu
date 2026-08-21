package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func spoke(text string) model.Session {
	return model.Session{Messages: []model.Message{{Role: "assistant", Text: text}}}
}

func TestPassHookGatesDropsWhatTheHookWouldNotInject(t *testing.T) {
	terms := []string{"pgbouncer", "shard"}

	// One mention of one term is a hint, not a subject.
	weak := spoke("pgbouncer looks fine on the shard")
	if got := passHookGates([]model.Session{weak}, []int{1}, nil, terms); len(got) != 0 {
		t.Errorf("one ordinary mention is not enough to inject, kept %d", len(got))
	}

	// A session that keeps returning to the term is admitted on that term alone,
	// which is the rule the hook ships (#1515).
	about := spoke(strings.Repeat("pgbouncer again ", 20))
	if got := passHookGates([]model.Session{about}, []int{1}, nil, terms); len(got) != 1 {
		t.Errorf("a session that repeats the term is about it, kept %d", len(got))
	}

	// Matched only where a tool printed the word: nobody spoke about it.
	quiet := model.Session{Messages: []model.Message{
		{Role: "tool-output", Text: strings.Repeat("pgbouncer shard restart ", 20)},
	}}
	if got := passHookGates([]model.Session{quiet}, []int{2}, nil, terms); len(got) != 0 {
		t.Errorf("a session that only matched inside tool output was kept: %d", len(got))
	}

	// The block carries at most two sessions.
	many := []model.Session{spoke("pgbouncer shard one"), spoke("pgbouncer shard two"),
		spoke("pgbouncer shard three")}
	if got := passHookGates(many, []int{2, 2, 2}, nil, terms); len(got) != 2 {
		t.Errorf("kept %d sessions; the block carries two", len(got))
	}
}
