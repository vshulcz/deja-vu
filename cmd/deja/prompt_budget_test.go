package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// The block can carry two sessions, each with its own quoted lines, and 1024
// bytes cut the second one short — taking the tail, which is where the paths,
// names and numbers live. Measured on a real store by replaying the sweep
// against what the agent actually answered next: blocks carrying words the
// answer went on to use rose from 20% to 25% when the budget grew to 1536, at
// 24 tokens a message, and 2048 bought nothing further.
func TestBudgetHoldsBothSessions(t *testing.T) {
	filler := strings.Repeat("pgbouncer pool ", 12)
	sessions := []model.Session{
		{
			Harness: "claude", ID: "first", Project: "proj",
			Messages: []model.Message{
				{Role: "user", Text: "что там с pgbouncer"},
				{Role: "assistant", Text: filler + "и держим 40 на шард"},
				{Role: "assistant", Text: "в итоге pgbouncer " + filler + "порог 0.7"},
			},
		},
		{
			Harness: "claude", ID: "second", Project: "proj",
			Messages: []model.Message{
				{Role: "user", Text: "pgbouncer снова"},
				{Role: "assistant", Text: filler + "и prepared statements выключены"},
				{Role: "assistant", Text: "в итоге pgbouncer " + filler + "режим transaction"},
			},
		},
	}
	got := search.AutoRecallDigestForAsked(sessions,
		promptHookBudget-recallFrameOverhead, []string{"pgbouncer"}, "что там с pgbouncer")
	if !strings.Contains(got, "second") {
		t.Fatalf("the second session did not fit at all:\n%s", got)
	}
	if !strings.Contains(got, "режим transaction") {
		t.Errorf("the budget cut the tail off the second session's conclusion:\n%s", got)
	}
}
