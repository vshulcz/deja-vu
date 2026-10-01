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
	for i := 11; i <= 15; i++ {
		body += rec("assistant", []any{
			map[string]any{"type": "tool_use", "id": fmt.Sprintf("r%d", i), "name": "Read", "input": map[string]any{"file_path": fmt.Sprintf("/repo/only-read-%d.go", i)}},
		}, i)
	}
	for i := 16; i <= 25; i++ {
		body += rec("assistant", []any{
			map[string]any{"type": "text", "text": "reading another file"},
			map[string]any{"type": "tool_use", "id": fmt.Sprintf("t%d", i), "name": "Edit", "input": map[string]any{
				"file_path": "/repo/idea.go", "old_string": fmt.Sprintf("old line %d", i), "new_string": fmt.Sprintf("\tSource Source // set to USER for every idea published from bot %d", i)}},
			map[string]any{"type": "tool_use", "id": fmt.Sprintf("m%d", i), "name": "Read", "input": map[string]any{"file_path": fmt.Sprintf("/repo/only-read-beside-%d.go", i)}},
			map[string]any{"type": "tool_use", "id": fmt.Sprintf("b%d", i), "name": "Bash", "input": map[string]any{"command": fmt.Sprintf("go test ./... # %d", i)}},
		}, i)
		body += rec("user", []any{map[string]any{"type": "tool_result", "tool_use_id": fmt.Sprintf("b%d", i), "content": "ok  repo 0.1s"}}, i)
	}
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
	if count[RoleCommand] > SubagentTailKept || count[RoleToolOutput] > SubagentTailKept {
		t.Errorf("the tool stream came back with the edits: %d commands, %d outputs", count[RoleCommand], count[RoleToolOutput])
	}
}
