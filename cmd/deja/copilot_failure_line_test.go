package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Copilot CLI 1.0.91 sends postToolUse for a command that exited non-zero,
// in camelCase, with the output under toolResult.textResultForLlm, and reads
// only a flat {"additionalContext": ...} back. Measured on a stand: that text
// is appended to the tool result the model reads in the same turn.
func TestFixPairAnswersCopilotPostToolUse(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	payload, _ := json.Marshal(map[string]any{
		"sessionId": "copilot-1",
		"cwd":       "/work/app",
		"toolName":  "bash",
		"toolArgs":  `{"command": "make"}`,
		"toolResult": map[string]any{
			"resultType":       "success",
			"textResultForLlm": "panic: sql: database is closed\n<shellId: 0 completed with exit code 2>",
		},
	})
	copilotHookOutput = true
	t.Cleanup(func() { copilotHookOutput = false })
	var out bytes.Buffer
	if err := runHookToolAfterMode(os.Getenv("DEJA_INDEX_DIR"), bytes.NewReader(payload), &out, false); err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not the flat shape Copilot reads: %q: %v", out.String(), err)
	}
	if len(got) != 1 || !strings.Contains(got["additionalContext"], "make clean && make CGO_ENABLED=0") {
		t.Fatalf("Copilot got no fix line, or more than additionalContext: %v", got)
	}
}

func TestCopilotInstallWiresTheFailureLine(t *testing.T) {
	for _, h := range copilotHooks {
		if h.event == "postToolUse" && strings.Join(h.args, " ") == "hook-tool-after --copilot" {
			return
		}
	}
	t.Fatal("copilot-auto does not wire postToolUse to hook-tool-after --copilot")
}
