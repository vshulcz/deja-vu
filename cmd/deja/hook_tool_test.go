package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func toolHookRun(t *testing.T, payload string) string {
	t.Helper()
	var out strings.Builder
	if err := runHookTool(os.Getenv("DEJA_INDEX_DIR"), strings.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// The hook fires on every action an agent takes, so its default is silence and
// its ceiling is one short line. A command this machine has run before earns
// that line; anything else does not.
func TestToolHookSpeaksOnlyForACommandWithAHistory(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"a", "b"} {
		writeClaudeFixture(t, filepath.Join(root, "p", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"run the suite"}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./... -count=1"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	out := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./... -count=1"},"session_id":"now"}`)
	if out == "" {
		t.Fatal("a command run in two sessions produced no line")
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("hook output is not the PreToolUse shape: %v (%q)", err, out)
	}
	if resp.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("wrong event name: %q", resp.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "2 sessions") {
		t.Errorf("the line does not say how widely it ran: %q", resp.HookSpecificOutput.AdditionalContext)
	}
	if n := len(resp.HookSpecificOutput.AdditionalContext); n > toolHookMaxBytes+len(frameRecall("")) {
		t.Errorf("payload is %d bytes, past the per-action budget", n)
	}

	// A command with no history at all is silence, not a "nothing found" line:
	// this is paid once per action.
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"terraform apply -auto-approve"},"session_id":"now"}`); got != "" {
		t.Errorf("spoke about a command it has never seen: %q", got)
	}
	// And an editor action on a file nothing has touched stays silent too.
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/srv/app/never.go"},"session_id":"now"}`); got != "" {
		t.Errorf("spoke about a file with no history: %q", got)
	}
	// Malformed input is a miss, never an error.
	if got := toolHookRun(t, "not json"); got != "" {
		t.Errorf("answered malformed input: %q", got)
	}
}
