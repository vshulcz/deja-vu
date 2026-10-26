package sources

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A child run that edits files is cut to its task and its answer like any
// other (#3009), but the edits are what blame, attribution and restore read.
// Dropping them with the prose meant blame never named the subagent that
// changed a file: 1,668 subagent edits on one machine, none indexed (#4163).
// The cut keeps the change records and still drops the reading in between.
func TestASubagentKeepsItsEditsWhenItsMiddleIsCut(t *testing.T) {
	sub := filepath.Join(t.TempDir(), "proj", "parent-1", "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := func(role string, content any, minute int) string {
		b, err := json.Marshal(map[string]any{
			"type": role, "sessionId": "parent-1", "isSidechain": true, "agentId": "child-1",
			"requestId": fmt.Sprintf("req-%d", minute),
			"timestamp": fmt.Sprintf("2026-08-02T10:%02d:00Z", minute),
			"message":   map[string]any{"role": role, "content": content},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	body := rec("user", "move Recommended out of idea.go", 0)
	var bodySb35 strings.Builder
	for i := 11; i <= 15; i++ {
		bodySb35.WriteString(rec("assistant", []any{
			map[string]any{"type": "tool_use", "id": fmt.Sprintf("r%d", i), "name": "Read", "input": map[string]any{"file_path": fmt.Sprintf("/repo/only-read-%d.go", i)}},
		}, i))
	}
	body += bodySb35.String()
	var bodySb40 strings.Builder
	for i := 16; i <= 25; i++ {
		bodySb40.WriteString(rec("assistant", []any{
			map[string]any{"type": "text", "text": "reading another file"},
			map[string]any{"type": "tool_use", "id": fmt.Sprintf("t%d", i), "name": "Edit", "input": map[string]any{
				"file_path": "/repo/idea.go", "old_string": fmt.Sprintf("old line %d", i), "new_string": fmt.Sprintf("\tSource Source // set to USER for every idea published from bot %d", i)}},
			map[string]any{"type": "tool_use", "id": fmt.Sprintf("m%d", i), "name": "Read", "input": map[string]any{"file_path": fmt.Sprintf("/repo/only-read-beside-%d.go", i)}},
			map[string]any{"type": "tool_use", "id": fmt.Sprintf("b%d", i), "name": "Bash", "input": map[string]any{"command": fmt.Sprintf("go test ./... # %d", i)}},
		}, i))
		bodySb40.WriteString(rec("user", []any{map[string]any{"type": "tool_result", "tool_use_id": fmt.Sprintf("b%d", i), "content": "ok  repo 0.1s"}}, i))
	}
	body += bodySb40.String()
	body += rec("assistant", "moved Recommended into recommended.go; tests pass", 30)
	path := filepath.Join(sub, "agent-child-1.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseClaudeFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions = %d, err %v", len(ss), err)
	}
	count := map[string]int{}
	var prose []string
	for _, m := range ss[0].Messages {
		count[m.Role]++
		if m.Role == "user" || m.Role == "assistant" {
			prose = append(prose, m.Text)
		}
	}
	if count[RoleEdit] != 10 {
		t.Errorf("edit records = %d, want all 10 the child made", count[RoleEdit])
	}
	if count[RoleWrote] != 10 {
		t.Errorf("wrote records = %d, want all 10", count[RoleWrote])
	}
	if count[RoleFiles] == 0 {
		t.Error("the files the child touched are gone")
	}
	for _, m := range ss[0].Messages {
		if m.Role == RoleFiles && strings.Contains(m.Text, "only-read-") {
			t.Errorf("a file the child only read came back: %q", m.Text)
		}
	}
	if n := strings.Count(strings.Join(prose, "\n"), "reading another file"); n > SubagentTailKept {
		t.Errorf("the middle's prose came back with the edits: %d turns", n)
	}
	// The commands stay, and `ok  repo 0.1s` is go test's own verdict (#5678).
	if count[RoleCommand] != 10 {
		t.Errorf("command records = %d, want all 10 the child ran", count[RoleCommand])
	}
}

// A child run's commands and the outputs that failed are what `fix` mines a
// failure and its remedy from. Cut with the prose, 1482 subagent sessions on one
// machine held no command record, and October's failure-and-fix episodes were
// nearly all in child runs (#5678).
func TestASubagentKeepsItsCommandsAndTheOutputsThatFailed(t *testing.T) {
	old := ChildOutputFailed
	ChildOutputFailed = func(s string) bool { return strings.Contains(s, "ERROR:") }
	t.Cleanup(func() { ChildOutputFailed = old })
	sub := filepath.Join(t.TempDir(), "proj", "parent-1", "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	minute := 0
	rec := func(role string, content any) string {
		minute++
		b, err := json.Marshal(map[string]any{
			"type": role, "sessionId": "parent-1", "isSidechain": true, "agentId": "child-1",
			"timestamp": fmt.Sprintf("2026-10-20T10:%02d:00Z", minute),
			"message":   map[string]any{"role": role, "content": content},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	var body strings.Builder
	body.WriteString(rec("user", "install vllm on the build box"))
	bash := func(id, cmd, out string, failed bool) {
		body.WriteString(rec("assistant", []any{
			map[string]any{"type": "text", "text": "running it"},
			map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": cmd}},
		}))
		body.WriteString(rec("user", []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": out, "is_error": failed}}))
	}
	read := func(id, path, out string) {
		body.WriteString(rec("assistant", []any{
			map[string]any{"type": "tool_use", "id": id, "name": "Read", "input": map[string]any{"file_path": path}},
		}))
		body.WriteString(rec("user", []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": out}}))
	}
	bash("b1", "git status", "nothing to commit, working tree clean", false)
	read("r1", "/repo/notes.txt", "ERROR: an old log line someone pasted into notes")
	bash("b2", "pip3 install vllm==0.6.3", "Collecting vllm==0.6.3\n  Downloading index page\nERROR: No matching distribution found for vllm==0.6.3", true)
	bash("b3", "pip3 install --index-url https://mirror.example.internal/simple vllm==0.6.3", "Successfully installed vllm-0.6.3", false)
	bash("b4", "go test ./...", "--- FAIL: TestX (0.00s)\nFAIL\trepo\t0.1s", true)
	bash("b5", "go test ./...", "ok  \trepo\t0.1s", false)
	for i := range 3 {
		body.WriteString(rec("assistant", fmt.Sprintf("closing turn %d", i)))
	}
	body.WriteString(rec("assistant", "vllm comes from the mirror; tests pass"))
	path := filepath.Join(sub, "agent-child-1.jsonl")
	if err := os.WriteFile(path, []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClaudeFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions = %d, err %v", len(ss), err)
	}
	var cmds, outs []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleCommand:
			cmds = append(cmds, m.Text)
		case RoleToolOutput:
			outs = append(outs, m.Text)
		case "user", "assistant":
			if m.Text == "running it" {
				t.Error("the middle's prose came back with the commands")
			}
		}
	}
	joined := strings.Join(cmds, "\n")
	for _, want := range []string{"$ pip3 install vllm==0.6.3", "mirror.example.internal/simple vllm==0.6.3  → exit 0", "git status"} {
		if !strings.Contains(joined, want) {
			t.Errorf("command %q is missing from the cut run:\n%s", want, joined)
		}
	}
	gotOuts := strings.Join(outs, "\n|\n")
	for _, want := range []string{"No matching distribution", "--- FAIL: TestX", "ok  \trepo\t0.1s"} {
		if !strings.Contains(gotOuts, want) {
			t.Errorf("output %q is missing:\n%s", want, gotOuts)
		}
	}
	for _, gone := range []string{"working tree clean", "Successfully installed", "old log line", "Downloading index page"} {
		if strings.Contains(gotOuts, gone) {
			t.Errorf("output %q came back with the failures", gone)
		}
	}
	if again := KeepSubagentTail(ss[0].Messages); len(again) != len(ss[0].Messages) {
		t.Errorf("a second cut changed the run: %d records, then %d", len(ss[0].Messages), len(again))
	}
}
