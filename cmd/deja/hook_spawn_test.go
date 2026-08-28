package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type spawnHookResponse struct {
	HookSpecificOutput struct {
		HookEventName      string                     `json:"hookEventName"`
		PermissionDecision string                     `json:"permissionDecision"`
		UpdatedInput       map[string]json.RawMessage `json:"updatedInput"`
	} `json:"hookSpecificOutput"`
}

// A spawned agent has no session start and sends no user prompt, so the only
// thing that reaches it is the instructions the parent wrote. Answering the
// spawn with additionalContext would hand the memory to the parent, which
// already has it; the reply has to rewrite the input.
func TestSpawnHookPutsRecallInsideTheSubagentsPrompt(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "app", "past.jsonl"), "past", []string{
		`{"type":"user","sessionId":"past","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the pgbouncer prepared statement error again"}}`,
		`{"type":"assistant","sessionId":"past","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":"We decided to set default_query_exec_mode=exec for pgbouncer, because prepared statements do not survive transaction pooling."}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// The recall is ranked inside the project the parent is working in, so the
	// spawn has to be answered from one.
	cwd := filepath.Join(t.TempDir(), "src", "app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	payload := `{"hook_event_name":"PreToolUse","tool_name":"Task","tool_input":` +
		`{"prompt":"Investigate the pgbouncer prepared statement failure in the orders service.",` +
		`"subagent_type":"general-purpose","description":"pgbouncer dig"},"session_id":"parent"}`
	out := toolHookRun(t, payload)
	if out == "" {
		t.Fatal("a spawn whose subject the store answers carried nothing")
	}
	var resp spawnHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("not the PreToolUse shape: %v (%q)", err, out)
	}
	if resp.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("wrong event name: %q", resp.HookSpecificOutput.HookEventName)
	}
	if resp.HookSpecificOutput.PermissionDecision != "allow" {
		t.Errorf("a memory must not turn a spawn into a prompt: %q",
			resp.HookSpecificOutput.PermissionDecision)
	}
	// Every field of the original input has to come back. A PreToolUse reply
	// replaces the input rather than merging into it, so a dropped field here
	// silently loses the agent type or the description the parent chose.
	for _, f := range []string{"prompt", "subagent_type", "description"} {
		if _, ok := resp.HookSpecificOutput.UpdatedInput[f]; !ok {
			t.Errorf("updatedInput dropped %q", f)
		}
	}
	var prompt string
	if err := json.Unmarshal(resp.HookSpecificOutput.UpdatedInput["prompt"], &prompt); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(prompt, "Investigate the pgbouncer prepared statement failure") {
		t.Errorf("the parent's instructions were not kept intact: %q", prompt[:60])
	}
	if !strings.Contains(prompt, "default_query_exec_mode") {
		t.Errorf("the recall did not reach the subagent's prompt: %q", prompt)
	}
}

// The tool that spawns an agent is called Task in Claude Code and Agent in
// hosts that renamed it. Both are the same event and both must be answered,
// because the wiring matches on the name.
func TestSpawnHookAnswersBothNames(t *testing.T) {
	for _, name := range []string{"Task", "Agent", "task", "subagent"} {
		if !isSpawnTool(name) {
			t.Errorf("%q is a spawn and was not recognised", name)
		}
	}
	for _, name := range []string{"Bash", "Edit", "Write", "TaskOutput"} {
		if isSpawnTool(name) {
			t.Errorf("%q is not a spawn", name)
		}
	}
}

// Silence is the default here as everywhere else: a spawn about work the store
// knows nothing about is left exactly as the parent wrote it.
func TestSpawnHookStaysSilentWithNothingToSay(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "app", "past.jsonl"), "past", []string{
		`{"type":"user","sessionId":"past","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"rename the changelog heading"}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Task","tool_input":`+
		`{"prompt":"Port the kubernetes admission webhook to the new cert rotation scheme."},"session_id":"parent"}`)
	if got != "" {
		t.Errorf("spoke about work it has no history of: %q", got)
	}
	// A spawn with no prompt at all is not something to answer either.
	got = toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Task","tool_input":{"description":"x"},"session_id":"parent"}`)
	if got != "" {
		t.Errorf("answered a spawn with no instructions: %q", got)
	}
}
