package digest

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func toolHeavySession() model.Session {
	base := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	msg := func(role, text string, min int) model.Message {
		return model.Message{Role: role, Text: text, Time: base.Add(time.Duration(min) * time.Minute)}
	}
	return model.Session{
		ID: "agent-run", Harness: "claude", Project: "app", Updated: base.Add(time.Hour),
		Messages: []model.Message{
			msg("user", "the exporter drops rows when the batch crosses midnight", 0),
			msg("assistant", "Running the test suite to reproduce the report now.", 1),
			msg("assistant", "Running the test suite to reproduce the report now.", 2),
			msg("assistant", "Running the test suite to reproduce the report now.", 3),
			msg("assistant", "Checking the batch window helpers for boundary handling.", 4),
			msg("assistant", "Root cause: the window used local midnight but rows are stamped UTC, so the fix pins the cutoff to UTC.", 5),
			msg("assistant", "All 42 tests pass, closing out.", 6),
		},
	}
}

func TestShareToolHeavyPrefersConclusions(t *testing.T) {
	out := Share(toolHeavySession(), 0)
	if !strings.Contains(out, "Root cause") || !strings.Contains(out, "closing out") {
		t.Fatalf("conclusion and outcome must survive:\n%s", out)
	}
	if strings.Contains(out, "Checking the batch window helpers") {
		t.Fatalf("unmarked status chatter must be dropped when conclusions exist:\n%s", out)
	}
	if strings.Count(out, "Running the test suite") > 0 {
		t.Fatalf("repeated status lines must not survive selection:\n%s", out)
	}
}

func TestShareConversationalUnchanged(t *testing.T) {
	s := model.Session{
		ID: "chat", Harness: "claude", Project: "app",
		Messages: []model.Message{
			{Role: "user", Text: "what port does the dev server use"},
			{Role: "assistant", Text: "It listens on 5173 by default."},
		},
	}
	out := Share(s, 0)
	if !strings.Contains(out, "5173") {
		t.Fatalf("short conversational sessions must keep everything:\n%s", out)
	}
}

func TestDedupeStatusKeepsFirstOccurrence(t *testing.T) {
	ms := []model.Message{
		{Role: "assistant", Text: "retrying the deploy step"},
		{Role: "assistant", Text: "retrying   the deploy step"},
		{Role: "assistant", Text: "deploy finished"},
	}
	got := dedupeStatus(ms)
	if len(got) != 2 || got[1].Text != "deploy finished" {
		t.Fatalf("dedupe wrong: %#v", got)
	}
}
