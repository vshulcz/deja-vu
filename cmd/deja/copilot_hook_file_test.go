package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The hook file under ~/.copilot/hooks is read by Copilot CLI and by VS Code
// Copilot Chat, and the two want different answers from the same command.
// Copilot CLI's events send camelCase and read only a flat additionalContext
// (measured on 1.0.92); VS Code sends hook_event_name and reads Claude's
// hookSpecificOutput (Copilot Chat 0.68: SessionStart, PreToolUse and
// PostToolUse read nothing else).

func withCopilotFlag(t *testing.T) {
	t.Helper()
	was, strict := copilotHookOutput, strictHookOutput
	copilotHookOutput = true
	t.Cleanup(func() { copilotHookOutput, strictHookOutput = was, strict })
}

// retryCfgHistory is a file three sessions changed, with the decision they
// reached, so the line before an edit has something to say.
func retryCfgHistory(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"e1", "e2", "e3"} {
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"change the retry count"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/retry.cfg","old_string":"retry = 5","new_string":"retry = 3"}}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"assistant","content":"We settled on three in retry.cfg; five retries overloaded the upstream."}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	return os.Getenv("DEJA_INDEX_DIR")
}

func TestCopilotEditGetsTheFileLineFlat(t *testing.T) {
	dir := retryCfgHistory(t)
	withCopilotFlag(t)
	// What Copilot CLI 1.0.92 sends for its edit tool.
	payload := `{"sessionId":"live","timestamp":1791367808754,"cwd":"/work/alpha","toolName":"edit","toolArgs":{"path":"/work/alpha/retry.cfg","old_str":"retry = 5","new_str":"retry = 4"}}`
	var out bytes.Buffer
	if err := runHookToolMode(dir, strings.NewReader(payload), &out, hookToolCopilot); err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || len(got) != 1 {
		t.Fatalf("not the flat shape Copilot CLI reads: %q (%v)", out.String(), err)
	}
	if !strings.Contains(got["additionalContext"], "overloaded the upstream") {
		t.Errorf("the edit got no file line: %q", got["additionalContext"])
	}
}

func TestVSCodeEditGetsTheFileLineNested(t *testing.T) {
	dir := retryCfgHistory(t)
	withCopilotFlag(t)
	// What VS Code 1.140 builds for a PreToolUse: its own tool name and input.
	payload := `{"timestamp":"2026-10-07T10:00:00.000Z","hook_event_name":"PreToolUse","session_id":"vs-1","transcript_path":"/x/GitHub.copilot-chat/transcripts/vs-1.jsonl","cwd":"/work/alpha","tool_name":"replace_string_in_file","tool_input":{"filePath":"/work/alpha/retry.cfg","oldString":"retry = 5","newString":"retry = 4"},"tool_use_id":"call_1"}`
	var out bytes.Buffer
	if err := runHookToolMode(dir, strings.NewReader(payload), &out, hookToolCopilot); err != nil {
		t.Fatal(err)
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("not JSON: %q", out.String())
	}
	if resp.HookSpecificOutput.HookEventName != "PreToolUse" || !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "overloaded the upstream") {
		t.Errorf("VS Code reads only hookSpecificOutput here, got %q", out.String())
	}
}

func TestCopilotPromptAnswersEachHostInItsShape(t *testing.T) {
	// A fresh store per host: the same question twice is a repeat to deja.
	ask := func(payload string) string {
		tmp := hermeticEnv(t)
		t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
		writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work-app", "old.jsonl"), "old", []string{
			`{"type":"user","sessionId":"old","cwd":"/work/app","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the glimmerquest cache misses on every cold start"}}`,
			`{"type":"assistant","sessionId":"old","cwd":"/work/app","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":"warming the glimmerquest cache at boot from the last snapshot fixed the cold start misses"}}`,
		})
		if _, err := captureRun(t, "index"); err != nil {
			t.Fatal(err)
		}
		dir := os.Getenv("DEJA_INDEX_DIR")
		withCopilotFlag(t)
		var out bytes.Buffer
		if err := runHookPromptMode(dir, strings.NewReader(payload), &out, false); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	cli := ask(`{"sessionId":"cli-1","timestamp":1791367544652,"cwd":"/work/app","prompt":"why does the glimmerquest cache miss on a cold start"}`)
	var flat map[string]string
	if err := json.Unmarshal([]byte(cli), &flat); err != nil || len(flat) != 1 || !strings.Contains(flat["additionalContext"], "warming the glimmerquest cache") {
		t.Fatalf("Copilot CLI wants a flat additionalContext with the recall, got %q", cli)
	}
	vs := ask(`{"timestamp":"2026-10-07T10:00:00.000Z","hook_event_name":"UserPromptSubmit","session_id":"vs-1","cwd":"/work/app","prompt":"why does the glimmerquest cache miss on a cold start"}`)
	var resp map[string]any
	if err := json.Unmarshal([]byte(vs), &resp); err != nil {
		t.Fatalf("not JSON: %q", vs)
	}
	if _, ok := resp["systemMessage"]; ok {
		t.Errorf("VS Code shows systemMessage as a warning; it went out: %q", vs)
	}
	hso, _ := resp["hookSpecificOutput"].(map[string]any)
	if ctx, _ := hso["additionalContext"].(string); hso["hookEventName"] != "UserPromptSubmit" || !strings.Contains(ctx, "warming the glimmerquest cache") {
		t.Errorf("VS Code got no nested recall: %q", vs)
	}
}

// VS Code's terminal tool reports a failure inside the result text, and the
// PostToolUse answer has to be the nested one.
func TestVSCodeTerminalFailureGetsTheFixPairNested(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	withCopilotFlag(t)
	payload := mustMarshal(t, map[string]any{
		"timestamp": "2026-10-07T10:00:00.000Z", "hook_event_name": "PostToolUse", "session_id": "vs-1",
		"cwd": "/work/app", "tool_name": "run_in_terminal", "tool_use_id": "call_2",
		"tool_input":    map[string]any{"command": "make", "explanation": "build", "isBackground": false},
		"tool_response": "panic: sql: database is closed\nCommand exited with code 2",
	})
	var out bytes.Buffer
	if err := runHookToolAfterMode(os.Getenv("DEJA_INDEX_DIR"), bytes.NewReader(payload), &out, false); err != nil {
		t.Fatal(err)
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("not JSON: %q", out.String())
	}
	if resp.HookSpecificOutput.HookEventName != "PostToolUse" || !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "make clean && make CGO_ENABLED=0") {
		t.Errorf("VS Code got no nested fix line: %q", out.String())
	}
}

// Cursor fires postToolUseFailure for a tool that errors, with the message
// under error_message.
func TestCursorToolFailureGetsTheFixPair(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	payload := mustMarshal(t, map[string]any{
		"conversation_id": "c-1", "hook_event_name": "postToolUseFailure", "cwd": "/work/app",
		"tool_name": "Shell", "tool_input": map[string]any{"command": "make"},
		"error_message": "panic: sql: database is closed", "failure_type": "error",
	})
	var out bytes.Buffer
	if err := runHookToolAfterMode(os.Getenv("DEJA_INDEX_DIR"), bytes.NewReader(payload), &out, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "make clean") {
		t.Errorf("no fix line for cursor's failure payload: %q", out.String())
	}
}

// PreCompact from either Copilot host names the event log the session is in;
// the turns about to be summarised are read from it and handed back after.
func TestCopilotCompactionIsCapturedFromItsEventLog(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	const id = "5b6c06d9-1f64-4b28-a6b1-4f1b23528483"
	cwd := mustMarshal(t, workspace)
	log := filepath.Join(t.TempDir(), "session-state", id, "events.jsonl")
	compactionWrite(t, log, strings.Join([]string{
		`{"type":"session.start","data":{"sessionId":"` + id + `","copilotVersion":"1.0.92","startTime":"2026-10-07T10:21:00Z","context":{"cwd":` + string(cwd) + `}},"id":"a","timestamp":"2026-10-07T10:21:00Z"}`,
		`{"type":"user.message","data":{"content":"fix the flaky retry test"},"id":"b","timestamp":"2026-10-07T10:21:01Z"}`,
		`{"type":"assistant.message","data":{"content":"I decided to pin the retry count to three in retry.cfg."},"id":"c","timestamp":"2026-10-07T10:21:02Z"}`,
	}, "\n")+"\n")
	// Copilot CLI 1.0.92 runs the PascalCase key with snake_case fields.
	withHookStdin(t, `{"hook_event_name":"PreCompact","session_id":"`+id+`","timestamp":"2026-10-07T10:21:03Z","cwd":`+string(cwd)+`,"transcript_path":`+jsonString(log)+`,"trigger":"auto","custom_instructions":""}`)
	runHookPrecompact(dir)
	if _, found, err := index.Compaction(dir, id, compactionWorkspace(workspace)); err != nil || !found {
		t.Fatalf("no compaction stored for the Copilot session: found=%v err=%v", found, err)
	}
	withCopilotFlag(t)
	var out bytes.Buffer
	payload := `{"sessionId":"` + id + `","cwd":` + string(cwd) + `,"toolName":"bash","toolArgs":{"command":"go test ./..."}}`
	if err := runHookToolMode(dir, strings.NewReader(payload), &out, hookToolCopilot); err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || !strings.Contains(got["additionalContext"], "fix the flaky retry test") {
		t.Errorf("the next tool call did not carry the packet flat: %q", out.String())
	}
}
