package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Command Code's hook payload names the tool by its internal name —
// shell_command, edit_file, write_file — while the matcher deja wires sees the
// uppercase display name. The hook fired on every shell and edit and said
// nothing, because it did not know these names (#4371).
func TestToolHookKnowsCommandCodeToolNames(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := 0; i < 6; i++ {
		id := "s" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/retry.cfg","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	commandRunWithAnOutcome(t, "go test ./... -count=1", "a", "b")
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/alpha")
	for _, name := range []string{"edit_file", "write_file"} {
		out := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"`+name+`","tool_display_name":"EDIT","tool_input":{"file_path":"/work/alpha/retry.cfg","old_string":"a","new_string":"b"},"session_id":"cc-`+name+`","cwd":"/work/alpha"}`)
		var resp sessionStartHookResponse
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("%s: no PreToolUse answer: %v (%q)", name, err, out)
		}
		if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "6 sessions") {
			t.Errorf("%s: the file's history did not come back: %q", name, resp.HookSpecificOutput.AdditionalContext)
		}
	}

	t.Setenv("CLAUDE_PROJECT_DIR", "/work/app")
	out := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"shell_command","tool_display_name":"SHELL","tool_input":{"command":"go test ./... -count=1"},"session_id":"cc-shell","cwd":"/work/app"}`)
	if !strings.Contains(out, "2 sessions") {
		t.Errorf("shell_command: the command's history did not come back: %q", out)
	}
	if !isCommandTool("shell_command") {
		t.Error("hook-tool-after does not treat shell_command as a command")
	}
}
