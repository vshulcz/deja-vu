package search

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func askedSession(texts ...string) model.Session {
	s := model.Session{ID: "s1", Harness: "claude", Project: "app"}
	for _, t := range texts {
		s.Messages = append(s.Messages, model.Message{Role: "user", Text: t})
	}
	return s
}

// The same question in other words is the case the exact-match counter cannot
// see, and the one a person most needs answered from memory.
func TestAskedBeforeMatchesTheQuestionNotTheWording(t *testing.T) {
	s := askedSession("why does the scheduler keep retrying the pgbouncer connection")
	got := AskedBefore(s, []string{"scheduler", "retrying", "pgbouncer"})
	if got == "" {
		t.Fatal("a repeat in other words was not recognised")
	}
	if got != "why does the scheduler keep retrying the pgbouncer connection" {
		t.Errorf("quoted the wrong line: %q", got)
	}
}

// A shared word or two is coincidence. The bar is three quarters of the shorter
// question, so a different question about the same component does not claim to
// be a repeat.
func TestAskedBeforeDoesNotClaimADifferentQuestion(t *testing.T) {
	s := askedSession("how does the scheduler pick a shard for a new tenant")
	if got := AskedBefore(s, []string{"scheduler", "retrying", "pgbouncer"}); got != "" {
		t.Errorf("a different question about the same component was called a repeat: %q", got)
	}
	// Two terms overlap by chance often enough that the claim reads as noise.
	if got := AskedBefore(askedSession("restart the pgbouncer pool"), []string{"restart", "pgbouncer"}); got != "" {
		t.Errorf("two words were enough to claim a repeat: %q", got)
	}
}

// The block quotes this back, so it has to read as the question rather than as
// the question plus whatever was pasted under it.
func TestAskedBeforeQuotesTheQuestionAlone(t *testing.T) {
	s := askedSession("why does the scheduler keep retrying pgbouncer\n\npanic: runtime error\n\tat main.go:14\n\tat run.go:88")
	got := AskedBefore(s, []string{"scheduler", "retrying", "pgbouncer"})
	if got != "why does the scheduler keep retrying pgbouncer" {
		t.Errorf("the pasted trace came with it: %q", got)
	}
}

// Only what a person said. The agent restating the question back is not the
// question being asked twice.
func TestAskedBeforeIgnoresTheAgentsOwnWords(t *testing.T) {
	s := model.Session{ID: "s1", Messages: []model.Message{
		{Role: "assistant", Text: "so the scheduler keeps retrying the pgbouncer connection because"},
	}}
	if got := AskedBefore(s, []string{"scheduler", "retrying", "pgbouncer"}); got != "" {
		t.Errorf("the agent's own restatement counted as a repeat: %q", got)
	}
}
