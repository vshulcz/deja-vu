package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedEditHistory indexes six sessions that edited /work/alpha/config.go and
// settled something about it, so the pre-edit line has a decision to name.
func seedEditHistory(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := 0; i < 6; i++ {
		id := "s" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"We settled on config.go loading the sentinel before any read."},{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/config.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	return os.Getenv("DEJA_INDEX_DIR")
}

// Kimi keeps only a block's reason from PreToolUse (runPreToolUse, 0.28.1),
// so the line about a file it edits waits for the session's next prompt. The
// payload is Kimi's: snake_case, the file under `path`.
func TestKimiEditLineRidesTheNextPrompt(t *testing.T) {
	dir := seedEditHistory(t)
	pre, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse", "session_id": "kimi-1", "cwd": "/work/alpha",
		"tool_name": "Edit", "tool_input": map[string]any{"path": "config.go", "old_string": "a", "new_string": "b"},
		"tool_call_id": "call_1",
	})
	if out := runDeferredHook(t, dir, "hook-tool", []string{"--defer"}, string(pre)); strings.TrimSpace(out) != "" {
		t.Fatalf("a deferred hook printed what Kimi would drop: %q", out)
	}
	prompt := func() string {
		p, _ := json.Marshal(map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": "kimi-1", "cwd": "/work/alpha", "prompt": "ok"})
		return runDeferredHook(t, dir, "hook-prompt", []string{"--plain"}, string(p))
	}
	if got := prompt(); !strings.Contains(got, "sentinel") {
		t.Fatalf("the next prompt did not carry the edit line: %q", got)
	}
	if again := prompt(); strings.Contains(again, "sentinel") {
		t.Fatalf("the edit line was delivered twice: %q", again)
	}
}

// Kiro drops preToolUse output (sendStdout:false), and its agent hooks may
// name no session. fs_write is the CLI's writer in --no-interactive.
func TestKiroEditLineRidesTheNextPromptByDirectory(t *testing.T) {
	dir := seedEditHistory(t)
	pre, _ := json.Marshal(map[string]any{
		"hook_event_name": "preToolUse", "cwd": "/work/alpha", "tool_name": "fs_write",
		"tool_input": map[string]any{"command": "str_replace", "path": "/work/alpha/config.go", "old_str": "a", "new_str": "b"},
	})
	if out := runDeferredHook(t, dir, "hook-tool", []string{"--defer"}, string(pre)); strings.TrimSpace(out) != "" {
		t.Fatalf("a deferred hook printed what Kiro would drop: %q", out)
	}
	p, _ := json.Marshal(map[string]any{"hook_event_name": "userPromptSubmit", "cwd": "/work/alpha", "prompt": "ok"})
	if got := runDeferredHook(t, dir, "hook-prompt", []string{"--plain"}, string(p)); !strings.Contains(got, "sentinel") {
		t.Fatalf("the next prompt did not carry the edit line: %q", got)
	}
}

func codewhaleEnv(t *testing.T, workspace string, env map[string]string) {
	t.Helper()
	for _, k := range []string{"DEEPSEEK_TOOL_NAME", "DEEPSEEK_TOOL_ARGS", "DEEPSEEK_TOOL_CALL_ID", "DEEPSEEK_TOOL_SUCCESS", "DEEPSEEK_TOOL_RESULT"} {
		t.Setenv(k, "")
	}
	t.Setenv("DEEPSEEK_SESSION_ID", "sess_1")
	t.Setenv("DEEPSEEK_WORKSPACE", workspace)
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func codewhaleHook(t *testing.T, dir, event, stdin string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runHookCodeWhale(dir, []string{event}, strings.NewReader(stdin), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// CodeWhale discards what tool_call_after prints. The fix pair for a failed
// shell command is parked there and rides the next tool_call_before, appended
// to that tool's result, once. The receipt is the stdin document 0.10.1 sends
// for a settled native shell call (docs/HOOKS.md).
func TestCodeWhaleFixPairRidesTheNextToolCall(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	dir := os.Getenv("DEJA_INDEX_DIR")
	codewhaleEnv(t, "/work/app", map[string]string{
		"DEEPSEEK_TOOL_NAME": "exec_shell", "DEEPSEEK_TOOL_CALL_ID": "call_1",
		"DEEPSEEK_TOOL_SUCCESS": "false", "DEEPSEEK_TOOL_RESULT": "goroutine 1 [running]:\npanic: sql: database is closed",
	})
	receipt := `{"schema_version":1,"event":"tool_call_after","tool_name":"exec_shell","session_id":"sess_1","tool_call_id":"call_1",` +
		`"execution_receipt":{"schema_version":1,"command":"make","cwd":"/work/app","completion":"failed","exit_code":2,` +
		`"stdout":"","stderr":"goroutine 1 [running]:\npanic: sql: database is closed","output_mode":"separate"}}`
	if out := codewhaleHook(t, dir, "tool_call_after", receipt); strings.TrimSpace(out) != "" {
		t.Fatalf("tool_call_after printed what CodeWhale discards: %q", out)
	}
	codewhaleEnv(t, "/work/app", map[string]string{"DEEPSEEK_TOOL_NAME": "read", "DEEPSEEK_TOOL_ARGS": `{"path":"README.md"}`, "DEEPSEEK_TOOL_CALL_ID": "call_2"})
	got := codewhaleHook(t, dir, "tool_call_before", "")
	var resp struct {
		AdditionalContext string `json:"additionalContext"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &resp); err != nil || !strings.Contains(resp.AdditionalContext, "make clean && make CGO_ENABLED=0") {
		t.Fatalf("the next tool call did not carry the fix pair: %q", got)
	}
	codewhaleEnv(t, "/work/app", map[string]string{"DEEPSEEK_TOOL_NAME": "read", "DEEPSEEK_TOOL_ARGS": `{"path":"README.md"}`, "DEEPSEEK_TOOL_CALL_ID": "call_3"})
	if again := codewhaleHook(t, dir, "tool_call_before", ""); strings.Contains(again, "CGO_ENABLED=0") {
		t.Fatalf("the fix pair was delivered twice: %q", again)
	}
}

// A call that fails to run goes back to the model as "Error: …" without the
// tool_call_before context (turn_loop.rs, 0.10.1). Its line waits for the next
// message then; a call that ran keeps nothing back.
func TestCodeWhaleLineOfACallThatFailedToRunRidesTheNextMessage(t *testing.T) {
	dir := seedEditHistory(t)
	before := func(id string) string {
		codewhaleEnv(t, "/work/alpha", map[string]string{"DEEPSEEK_TOOL_NAME": "edit", "DEEPSEEK_TOOL_ARGS": `{"path":"config.go"}`, "DEEPSEEK_TOOL_CALL_ID": id})
		return codewhaleHook(t, dir, "tool_call_before", "")
	}
	if got := before("call_1"); !strings.Contains(got, "sentinel") {
		t.Fatalf("tool_call_before said nothing about the file: %q", got)
	}
	codewhaleEnv(t, "/work/alpha", map[string]string{"DEEPSEEK_TOOL_NAME": "edit", "DEEPSEEK_TOOL_CALL_ID": "call_1",
		"DEEPSEEK_TOOL_SUCCESS": "false", "DEEPSEEK_TOOL_RESULT": "Failed to validate input: old_string not found"})
	codewhaleHook(t, dir, "tool_call_after", "")
	submit := func() string {
		return codewhaleHook(t, dir, "message_submit", `{"event":"message_submit","text":"try again","session_id":"sess_1","workspace":"/work/alpha"}`)
	}
	if got := submit(); !strings.Contains(got, "sentinel") {
		t.Fatalf("the next message did not carry the dropped line: %q", got)
	}
	if again := submit(); strings.Contains(again, "sentinel") {
		t.Fatalf("the line was delivered twice: %q", again)
	}

	// A call that ran: its line reached the model, so nothing is kept.
	codewhaleEnv(t, "/work/alpha", map[string]string{"DEEPSEEK_SESSION_ID": "sess_2", "DEEPSEEK_TOOL_NAME": "edit",
		"DEEPSEEK_TOOL_ARGS": `{"path":"config.go"}`, "DEEPSEEK_TOOL_CALL_ID": "call_9"})
	if got := codewhaleHook(t, dir, "tool_call_before", ""); !strings.Contains(got, "sentinel") {
		t.Fatalf("tool_call_before said nothing about the file: %q", got)
	}
	codewhaleEnv(t, "/work/alpha", map[string]string{"DEEPSEEK_SESSION_ID": "sess_2", "DEEPSEEK_TOOL_NAME": "edit",
		"DEEPSEEK_TOOL_CALL_ID": "call_9", "DEEPSEEK_TOOL_SUCCESS": "true", "DEEPSEEK_TOOL_RESULT": "edited"})
	codewhaleHook(t, dir, "tool_call_after", "")
	if hasDeferredFor(dir, codewhaleCallKey("call_9")) || hasDeferredFor(dir, deferredKey("sess_2", "/work/alpha")) {
		t.Fatal("a call that ran left its line parked")
	}
}
