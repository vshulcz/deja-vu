package main

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// grokUpdates writes a Grok Build 1.0.41 session the way the CLI keeps it:
// summary.json beside the ACP updates.jsonl, under the URL-encoded working
// directory. The lines are the ones a headless run wrote on a stand: the shell
// call arrives as tool_call, then a tool_call_update carrying the title and the
// description, then one carrying the output — three records for one command.
func grokUpdates(t *testing.T, workspace, id string) string {
	t.Helper()
	// Grok names the group after the escaped cwd, which on Windows keeps the
	// drive's colon and is no directory name; the .cwd file beside it is the
	// other place it records the directory, and the one read first.
	group := url.PathEscape(workspace)
	if runtime.GOOS == "windows" {
		group = "ws"
	}
	compactionWrite(t, filepath.Join(sources.GrokRoot(), "sessions", group, ".cwd"), workspace)
	dir := filepath.Join(sources.GrokRoot(), "sessions", group, id)
	summary, _ := json.Marshal(map[string]any{
		"info":              map[string]any{"id": id, "cwd": workspace},
		"session_summary":   "fix the parser test",
		"created_at":        "2026-10-07T10:20:23.521115Z",
		"updated_at":        "2026-10-07T10:20:28.119804Z",
		"session_kind":      "headless",
		"generated_title":   "fix the parser test",
		"current_model_id":  "grok-build",
		"last_turn_summary": "The parser test fails: want 3, got 4.",
	})
	compactionWrite(t, filepath.Join(dir, "summary.json"), string(summary))
	rec := func(ts int64, update string) string {
		return `{"timestamp":` + jsonInt(ts) + `,"method":"session/update","params":{"sessionId":"` + id + `","update":` + update + `,"_meta":{"agentTimestampMs":` + jsonInt(ts*1000) + `}}}`
	}
	lines := []string{
		rec(1791368423, `{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"fix the parser test"},"_meta":{"modelId":"grok-build","promptIndex":0}}`),
		rec(1791368424, `{"sessionUpdate":"tool_call","toolCallId":"call_1","title":"run_terminal_command","rawInput":{"command":"go test ./parser/...","description":"run the parser tests"},"_meta":{"x.ai/tool":{"version":1,"name":"run_terminal_command","kind":"execute","namespace":"grok_build","label":"Run Command","read_only":false}}}`),
		rec(1791368424, `{"sessionUpdate":"tool_call_update","toolCallId":"call_1","kind":"execute","title":"Execute `+"`go test ./parser/...`"+`","content":[{"type":"content","content":{"type":"text","text":"run the parser tests"}}],"locations":[],"rawInput":{"variant":"Bash","command":"go test ./parser/...","description":"run the parser tests","is_background":false},"_meta":{"x.ai/tool":{"version":1,"name":"run_terminal_command","kind":"execute"}}}`),
		rec(1791368425, `{"sessionUpdate":"tool_call_update","toolCallId":"call_1","status":"completed","content":[{"type":"content","content":{"type":"text","text":"--- FAIL: TestParseSeed (0.00s)\n    parse_test.go:5: want 3, got 4\nFAIL\nFAIL\texample.com/ws/parser\t0.210s\nFAIL\n"}}],"rawOutput":{"type":"Bash","exit_code":1,"command":"go test ./parser/...","truncated":false,"timed_out":false}}`),
		rec(1791368425, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."},"_meta":{"promptIndex":0}}`),
		rec(1791368425, `{"sessionUpdate":"turn_completed","prompt_id":"p1","stop_reason":"end_turn","elapsed_ms":1167}`),
	}
	path := filepath.Join(dir, "updates.jsonl")
	compactionWrite(t, path, strings.Join(lines, "\n")+"\n")
	return path
}

func jsonInt(n int64) string { b, _ := json.Marshal(n); return string(b) }

// Grok Build's PreCompact names updates.jsonl in both spellings. Grok throws
// away what the prompt hook prints, so the packet goes out on the next
// PreToolUse, whose additionalContext Grok hands the model with the result.
func TestGrokCompactionPacketRidesTheNextToolCall(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	// The store where Grok keeps it, under the home: Grok names the file by
	// the path it resolved, which on macOS is /private/var/... for a home
	// under /var, and a root spelled through the link did not match it.
	t.Setenv("DEJA_GROK_ROOT", "")
	t.Setenv("GROK_HOME", "")
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	const id = "66666666-6666-4666-8666-666666666666"
	path := grokUpdates(t, workspace, id)
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}

	pre, _ := json.Marshal(map[string]any{
		"hookEventName": "pre_compact", "sessionId": id, "cwd": workspace, "workspaceRoot": workspace + "/",
		"transcriptPath": path, "permissionMode": "default", "source": "manual",
		"hook_event_name": "PreCompact", "session_id": id, "transcript_path": path,
	})
	t.Setenv("GROK_HOOK_EVENT", "pre_compact")
	withHookStdin(t, string(pre))
	runHookPrecompact(dir)
	state, found, err := index.Compaction(dir, id, workspace)
	if err != nil || !found {
		t.Fatalf("precompact stored nothing for a Grok session: %v", err)
	}
	if state.Harness != "grok" {
		t.Errorf("the capture is filed under %q, want grok", state.Harness)
	}

	// The prompt hook answers nothing on Grok: what it has waits for the
	// session's next tool hook (hook_deferred.go).
	t.Setenv("GROK_HOOK_EVENT", "user_prompt_submit")
	prompt, _ := json.Marshal(map[string]any{"sessionId": id, "session_id": id, "cwd": workspace, "prompt": "carry on"})
	withHookStdin(t, string(prompt))
	if out := captureStdout(t, func() { _ = runHookDeferred(dir, "hook-prompt", nil, commands["hook-prompt"]) }); strings.Contains(out, "additionalContext") {
		t.Fatalf("Grok's prompt hook answered: %s", out)
	}

	t.Setenv("GROK_HOOK_EVENT", "pre_tool_use")
	tool, _ := json.Marshal(map[string]any{
		"hookEventName": "pre_tool_use", "sessionId": id, "cwd": workspace, "workspaceRoot": workspace + "/",
		"transcriptPath": path, "toolName": "run_terminal_command", "toolUseId": "call_2",
		"toolInput":       map[string]any{"command": "go test ./parser/...", "description": "run it"},
		"hook_event_name": "PreToolUse", "session_id": id, "transcript_path": path,
		"tool_name": "run_terminal_command", "tool_input": map[string]any{"command": "go test ./parser/...", "description": "run it"}, "tool_use_id": "call_2",
	})
	withHookStdin(t, string(tool))
	out := captureStdout(t, func() { _ = runHookDeferred(dir, "hook-tool", nil, commands["hook-tool"]) })
	var response sessionStartHookResponse
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatalf("PreToolUse reply: %v %s", err, out)
	}
	if response.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("reply names %q", response.HookSpecificOutput.HookEventName)
	}
	wantCompactionPacket(t, response.HookSpecificOutput.AdditionalContext)
}
