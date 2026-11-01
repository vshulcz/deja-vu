package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestIsSmokeTestKeepsRealSessions(t *testing.T) {
	if !isSmokeTest("Reply with the single word OK.", []string{"OK"}) {
		t.Error("a token asked for and a token returned is a harness check")
	}
	if !isSmokeTest("скажи ок", []string{"Ок!"}) {
		t.Error("same in Russian")
	}
	// A short but real exchange is memory worth having.
	if isSmokeTest("why does the pool exhaust under load?", []string{"pgx caches statements per connection"}) {
		t.Error("a real question with a real answer must survive")
	}
	// A question with no answer recorded is still what someone asked.
	if isSmokeTest("the original digest content marker_alpha", nil) {
		t.Error("no reply is not the same as a token reply")
	}
	// The prompt shape alone is not enough: a real answer means real work.
	if isSmokeTest("reply with your assessment of the retry budget", []string{
		"the budget is wrong: it retries four times inside a one second deadline"}) {
		t.Error("a substantial answer means this was not a smoke test")
	}
}

func TestBuildAutoRecallSingleMarathonSessionScalesConclusionBudget(t *testing.T) {
	now := time.Now().UTC()
	s := model.Session{
		ID:      "marathon-1",
		Harness: "claude",
		Project: "app",
		Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "wire up the marathon subsystem", Time: now},
			{Role: "assistant", Text: "we decided alpha retry cap is 4", Time: now},
			{Role: "assistant", Text: "settled at beta batch size 64", Time: now},
			{Role: "assistant", Text: "settled that gamma pool mode is transaction", Time: now},
			{Role: "assistant", Text: "agreed to delta timeout of 30s", Time: now},
			{Role: "assistant", Text: "we chose epsilon compression zstd", Time: now},
			{Role: "assistant", Text: "we picked zeta flush interval 500ms", Time: now},
		},
	}
	gotSafe := BuildAutoRecall([]model.Session{s}, AutoRecallOptions{Mode: RecallSafe, ProjectNames: []string{"app"}, Now: now})
	for _, fact := range []string{"beta", "gamma", "delta", "epsilon"} {
		if !strings.Contains(gotSafe.Text, fact) {
			t.Errorf("single marathon session safe recall missed %q:\n%s", fact, gotSafe.Text)
		}
	}
	gotAgg := BuildAutoRecall([]model.Session{s}, AutoRecallOptions{Mode: RecallAggressive, ProjectNames: []string{"app"}, Now: now})
	for _, fact := range []string{"alpha", "beta", "gamma", "delta", "epsilon"} {
		if !strings.Contains(gotAgg.Text, fact) {
			t.Errorf("single marathon session aggressive recall missed %q:\n%s", fact, gotAgg.Text)
		}
	}
	s2 := s
	s2.ID = "marathon-2"
	s2.Messages = []model.Message{
		{Role: "user", Text: "second session", Time: now},
		{Role: "assistant", Text: "we decided theta cache ttl is 10m", Time: now},
	}
	gotPair := BuildAutoRecall([]model.Session{s, s2}, AutoRecallOptions{Mode: RecallSafe, ProjectNames: []string{"app"}, Now: now})
	if !strings.Contains(gotPair.Text, "theta") {
		t.Errorf("two-session recall missed second session:\n%s", gotPair.Text)
	}
}
