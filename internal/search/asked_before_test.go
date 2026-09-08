package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/prompt"
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

// Two task notifications share every word; that is the host repeating itself,
// not a question asked twice. A person's question with a reminder appended is
// still the question, and the quote is the question alone (#3156, #3157).
func TestAskedBeforeSkipsTheHostsOwnLines(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "<task-notification>\n<task-id>br50mykp6</task-id>\n<status>failed</status>\n<summary>Background command failed with exit code 2</summary>\n</task-notification>"},
		{Role: "user", Text: "Summary:\n1. Primary Request and Intent:\n   - fix the exporter_batch rows dropped at utc_midnight"},
	}}
	if got := AskedBefore(s, prompt.Terms("<task-notification>\n<task-id>x</task-id>\n<status>failed</status>\n<summary>Background command failed with exit code 2</summary>\n</task-notification>")); got != "" {
		t.Fatalf("a notification was asked before: %q", got)
	}
	if got := AskedBefore(s, prompt.Terms("fix the exporter_batch rows dropped at utc_midnight")); got != "" {
		t.Fatalf("a compaction summary was asked before: %q", got)
	}
	s.Messages = append(s.Messages, model.Message{Role: "user", Text: "why does exporter_batch drop rows at utc_midnight?\n<system-reminder>\nthe file changed on disk\n</system-reminder>"})
	got := AskedBefore(s, prompt.Terms("why does exporter_batch drop rows at utc_midnight"))
	if !strings.HasPrefix(got, "why does exporter_batch") || strings.Contains(got, "system-reminder") {
		t.Fatalf("the person's question with a reminder appended: %q", got)
	}
}
