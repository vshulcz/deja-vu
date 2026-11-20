package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestStripToolProtocolNoise(t *testing.T) {
	plain := "Plain assistant text without protocol markers."
	if got := StripToolProtocolNoise(plain); got != plain {
		t.Fatalf("expected fast-path untouched text, got %q", got)
	}

	raw := `<system-reminder>internal note</system-reminder>
[thinking]
Considering where the retry config lives.
Still checking options.

[tool_use Read]
{"file_path": "pkg/client/retry.go"}
[tool_result]
RETRY ERROR: deadline exceeded after 30s
<function_calls>
<invoke name="Read"/>
</function_calls>

Adjusted backoff cap to 30s.`
	got := StripToolProtocolNoise(raw)
	for _, bad := range []string{"system-reminder", "[thinking]", "Considering where", "[tool_use", `{"file_path"`, "[tool_result]", "function_calls"} {
		if strings.Contains(got, bad) {
			t.Fatalf("expected %q to be stripped, got:\n%s", bad, got)
		}
	}
	for _, want := range []string{"RETRY ERROR: deadline exceeded after 30s", "Adjusted backoff cap to 30s."} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q to be preserved, got:\n%s", want, got)
		}
	}
}

func TestIsPureProcessNarration(t *testing.T) {
	for _, s := range []string{
		"Let me check the configuration files next.",
		"I'll read that module now.",
		"Now let's check how it initializes.",
	} {
		if !IsPureProcessNarration(s) {
			t.Errorf("IsPureProcessNarration(%q) = false, want true", s)
		}
	}
	for _, s := range []string{
		"Let me check `pkg/client/retry.go` for timeout handling.",
		"Let me check why the test fails on timeout.",
		"I'll inspect the error trace from the worker.",
		"We fixed the timeout because retries overflowed.",
	} {
		if IsPureProcessNarration(s) {
			t.Errorf("IsPureProcessNarration(%q) = true, want false", s)
		}
	}
}

func TestWithoutHarnessEnvelopesCleansAssistantNoiseAndDampsNarration(t *testing.T) {
	s := model.Session{
		ID: "clean-1",
		Messages: []model.Message{
			{Role: "user", Text: "<system-reminder>hidden</system-reminder>Why did retry stall?"},
			{Role: "assistant", Text: "[thinking]\nChecking files.\n\n[tool_use Read]\n{\"file_path\":\"retry.go\"}"},
			{Role: "assistant", Text: "Let me check the retry module next."},
			{Role: "assistant", Text: "[tool_result] Error: retry stall on backoff overflow."},
		},
	}
	cleaned := withoutHarnessEnvelopes(s)
	if len(cleaned.Messages) != 3 {
		t.Fatalf("expected pure tool/thinking turn dropped leaving 3 messages, got %d: %+v", len(cleaned.Messages), cleaned.Messages)
	}
	if strings.Contains(cleaned.Messages[2].Text, "[tool_result]") {
		t.Fatalf("expected [tool_result] stripped from turn, got %q", cleaned.Messages[2].Text)
	}

	turns := []contextTurn{
		{role: "assistant", raw: "Let me check the retry module next.", hits: []int{2}},
		{role: "assistant", raw: "Retry stall happens because RetryBudget overflows.", hits: []int{2}},
	}
	w := contextTurnWeights(turns, 1)
	if !(w[0] < w[1]) {
		t.Fatalf("expected pure narration turn to be damped below substantive turn: w=%v", w)
	}
}
