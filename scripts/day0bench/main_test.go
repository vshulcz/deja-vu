package main

import (
	"slices"
	"testing"
)

func TestDedupeCorpusCollapsesIdenticalTurnListsAndRewritesAnswerIDs(t *testing.T) {
	shared := []turn{
		{Role: "user", Content: "Which database migration tool did we pick?"},
		{Role: "assistant", Content: "We settled on goose for SQL migrations."},
	}
	other := []turn{
		{Role: "user", Content: "How do we run linter checks?"},
		{Role: "assistant", Content: "Use golangci-lint run."},
	}

	all := []question{
		{
			ID:            "q1",
			Question:      "Which migration tool did we pick?",
			AnswerSession: []string{"answer_q1"},
			SessionIDs:    []string{"distractor_shared", "other_1"},
			Sessions:      [][]turn{shared, other},
			Dates:         []string{"2026/10/01 (Thu) 10:00", "2026/10/02 (Fri) 11:00"},
		},
		{
			ID:            "q2",
			Question:      "Remind me of the SQL migration tool.",
			AnswerSession: []string{"answer_q1", "dup_alias"},
			SessionIDs:    []string{"answer_q1", "dup_alias"},
			Sessions:      [][]turn{shared, shared},
			Dates:         []string{"2026/10/03 (Sat) 12:00", "2026/10/04 (Sun) 13:00"},
		},
	}
	qs := []question{all[1]}

	dedupeCorpus(all, qs)

	if got := all[0].SessionIDs[0]; got != "distractor_shared" {
		t.Fatalf("first occurrence canonical ID = %q, want distractor_shared", got)
	}
	for i, got := range all[1].SessionIDs {
		if got != "distractor_shared" {
			t.Fatalf("all[1].SessionIDs[%d] = %q, want canonical distractor_shared", i, got)
		}
	}
	if want := []string{"distractor_shared"}; !slices.Equal(qs[0].AnswerSession, want) {
		t.Fatalf("qs[0].AnswerSession = %v, want %v", qs[0].AnswerSession, want)
	}
}
