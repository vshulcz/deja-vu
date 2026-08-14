package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Before a command that failed here last time, the count is not the useful
// part — the cause is. The file path already carries the prior decision for
// exactly this reason; the command path said only how often it had been run.
func TestToolHookCommandLineCarriesWhatHappenedLastTime(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// Two sessions ran it; the second says how it was resolved.
	writeClaudeFixture(t, filepath.Join(root, "alpha", "run1.jsonl"), "run1", []string{
		`{"type":"user","sessionId":"run1","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"build it"}}`,
		`{"type":"assistant","sessionId":"run1","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"pnpm run bundle"}}]}}`,
	})
	writeClaudeFixture(t, filepath.Join(root, "alpha", "run2.jsonl"), "run2", []string{
		`{"type":"user","sessionId":"run2","cwd":"/work/alpha","timestamp":"2026-01-03T03:04:05Z","message":{"role":"user","content":"pnpm run bundle is failing again"}}`,
		`{"type":"assistant","sessionId":"run2","cwd":"/work/alpha","timestamp":"2026-01-03T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"pnpm run bundle"}}]}}`,
		`{"type":"assistant","sessionId":"run2","cwd":"/work/alpha","timestamp":"2026-01-03T03:04:07Z","message":{"role":"assistant","content":[{"type":"text","text":"Decision: pnpm run bundle needs the ARENAGUARD lockfile refreshed first."}]}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/alpha")
	out := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"pnpm run bundle"},"session_id":"now","cwd":"/work/alpha"}`)
	if out == "" {
		t.Fatal("no line for a command with history")
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	ctx := resp.HookSpecificOutput.AdditionalContext
	if !strings.Contains(ctx, "ARENAGUARD") {
		t.Errorf("the line says the command has run before but not what happened:\n%s", ctx)
	}
	if !strings.Contains(ctx, "last time:") {
		t.Errorf("the outcome is not labelled as the prior one:\n%s", ctx)
	}
}

// A command whose history holds no conclusion keeps the plain count rather
// than borrowing a decision from a session about something else.
func TestToolHookCommandLineStaysACountWithoutAConclusion(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "alpha", "quiet.jsonl"), "quiet", []string{
		`{"type":"user","sessionId":"quiet","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"run it"}}`,
		`{"type":"assistant","sessionId":"quiet","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"pnpm run bundle"}}]}}`,
	})
	// A session that decided something unrelated, which must not be borrowed.
	writeClaudeFixture(t, filepath.Join(root, "alpha", "other.jsonl"), "other", []string{
		`{"type":"user","sessionId":"other","cwd":"/work/alpha","timestamp":"2026-01-03T03:04:05Z","message":{"role":"user","content":"the avatar flashes on reload"}}`,
		`{"type":"assistant","sessionId":"other","cwd":"/work/alpha","timestamp":"2026-01-03T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"Decision: the ARENAGUARD image needs an explicit width."}]}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/alpha")
	out := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"pnpm run bundle"},"session_id":"now","cwd":"/work/alpha"}`)
	if out == "" {
		return // saying nothing is also acceptable here
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if ctx := resp.HookSpecificOutput.AdditionalContext; strings.Contains(ctx, "ARENAGUARD") {
		t.Errorf("a decision about something else was attached to the command:\n%s", ctx)
	}
}
