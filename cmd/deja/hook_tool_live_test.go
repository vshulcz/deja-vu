package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The session making the edit is in the index too once it has touched the
// file, and being the newest it was the one quoted: the agent was handed its
// own last reply as the file's history (#4380). The hook marks that session
// live before it answers, so the line is drawn from the other sessions only.
func TestToolHookLeavesTheLiveSessionOutOfTheFileLine(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	edit := func(id, ts, said string) {
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"` + ts + `","message":{"role":"user","content":"change the retry count"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/retry.cfg","old_string":"retry = 5","new_string":"retry = 3"}}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"` + ts + `","message":{"role":"assistant","content":"` + said + `"}}`,
		})
	}
	for i := 0; i < 5; i++ {
		edit("old"+string(rune('0'+i)), "2026-01-0"+string(rune('2'+i))+"T03:04:05Z",
			"We settled on three in retry.cfg; five retries overloaded the upstream.")
	}
	edit("live", "2026-02-01T03:04:05Z", "We settled on five in retry.cfg and stopped there.")
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/alpha")
	out := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/work/alpha/retry.cfg","old_string":"retry = 5","new_string":"retry = 4"},"session_id":"live","cwd":"/work/alpha"}`)
	if strings.Contains(out, "6 sessions") || strings.Contains(out, "stopped there") {
		t.Errorf("the line counted or quoted the session the edit is made in:\n%s", out)
	}
	if !strings.Contains(out, "5 sessions") || !strings.Contains(out, "overloaded the upstream") {
		t.Errorf("the other sessions did not speak for the file:\n%s", out)
	}

	// Control: from another session the live one is just another session that
	// worked on the file.
	out = toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/work/alpha/retry.cfg","old_string":"retry = 5","new_string":"retry = 4"},"session_id":"other","cwd":"/work/alpha"}`)
	if !strings.Contains(out, "6 sessions") || !strings.Contains(out, "stopped there") {
		t.Errorf("control: the newest session no longer speaks to other sessions:\n%s", out)
	}
}

// The command line had the same hole: the newest session to run a command is
// the one whose conclusion is quoted, and that is the live session once it has
// run the command and been indexed (#4380).
func TestToolHookLeavesTheLiveSessionOutOfTheCommandLine(t *testing.T) {
	t.Setenv("DEJA_COMMAND_HINTS", "on")
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	run := func(id, ts, said string) {
		writeClaudeFixture(t, filepath.Join(root, "-work-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts + `","message":{"role":"user","content":"should we run the orders migration against production?"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"make migrate-orders"}}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts + `","message":{"role":"assistant","content":"` + said + `"}}`,
		})
	}
	run("old", "2026-01-02T03:04:05Z", "no: run the orders migration against the replica first, production locks the table.")
	run("live", "2026-02-01T03:04:05Z", "decided: ran the orders migration straight on production this time.")
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/app")
	out := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"make migrate-orders"},"session_id":"live","cwd":"/work/app"}`)
	if strings.Contains(out, "straight on production") || strings.Contains(out, "2 sessions") {
		t.Errorf("the command line counted or quoted the session it fires in:\n%s", out)
	}
	if !strings.Contains(out, "replica first") {
		t.Errorf("the other session did not speak for the command:\n%s", out)
	}
	// Control: from another session the live one is history like any other.
	if out := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"make migrate-orders"},"session_id":"other","cwd":"/work/app"}`); !strings.Contains(out, "straight on production") {
		t.Errorf("control: the newest session no longer speaks to other sessions:\n%s", out)
	}
}
