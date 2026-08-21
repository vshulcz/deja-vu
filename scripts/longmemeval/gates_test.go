package main

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func spoke(text string) model.Session {
	return model.Session{Messages: []model.Message{{Role: "assistant", Text: text}}}
}

func TestPassHookGatesDropsWhatTheHookWouldNotInject(t *testing.T) {
	terms := []string{"pgbouncer", "shard"}
	weak := spoke("pgbouncer looks fine on the shard")
	got := passHookGates([]model.Session{weak}, []int{1}, []int{0}, terms)
	if len(got) != 0 {
		t.Errorf("one ordinary term is not enough to inject, kept %d", len(got))
	}

	got = passHookGates([]model.Session{weak}, []int{1}, []int{1}, terms)
	if len(got) != 1 {
		t.Errorf("a term rare enough to identify something earns an injection, kept %d", len(got))
	}

	// Matched only where a tool printed the word: nobody spoke about it.
	quiet := model.Session{Messages: []model.Message{
		{Role: "tool-output", Text: "pgbouncer shard restart"},
	}}
	if got := passHookGates([]model.Session{quiet}, []int{2}, []int{1}, terms); len(got) != 0 {
		t.Errorf("a session that only matched inside tool output was kept: %d", len(got))
	}

	// The block carries at most two sessions.
	many := []model.Session{spoke("pgbouncer shard one"), spoke("pgbouncer shard two"),
		spoke("pgbouncer shard three")}
	if got := passHookGates(many, []int{2, 2, 2}, []int{1, 1, 1}, terms); len(got) != 2 {
		t.Errorf("kept %d sessions; the block carries two", len(got))
	}
}
