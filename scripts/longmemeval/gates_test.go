package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func spoke(text string) model.Session {
	return model.Session{Messages: []model.Message{{Role: "assistant", Text: text}}}
}

// The benchmark applies the product's bar rather than a copy of it: a question
// naming something identifiable earns an injection on one match, a question
// made of ordinary words needs two.
func TestPassHookGatesAppliesTheProductBar(t *testing.T) {
	terms := []string{"pgbouncer", "shard"}
	if got := passHookGates([]model.Session{spoke("pgbouncer on the shard")},
		[]int{1}, nil, terms); len(got) != 1 {
		t.Errorf("a question naming pgbouncer earns an injection on one match, kept %d", len(got))
	}
	ordinary := []string{"build"}
	if got := passHookGates([]model.Session{spoke("build for dinner")},
		[]int{1}, nil, ordinary); len(got) != 0 {
		t.Errorf("one ordinary word is not a subject on its own: kept %d", len(got))
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
