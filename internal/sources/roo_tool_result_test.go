package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Roo puts a failing command's output in the tool_result block of the next
// user turn; the reader took text blocks only, so the error reached neither
// search nor the fix pairs (#3269).
func TestRooToolResultIsIndexedAsToolOutput(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks", "1788845325718")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `[
	 {"role":"user","content":[{"type":"text","text":"<task>\nrun go test and fix whatever fails in widgetd\n</task>"},{"type":"text","text":"<environment_details>\n# Current Cost\n$0.04\n</environment_details>"}]},
	 {"role":"assistant","content":[{"type":"text","text":"I'll run the tests first."},{"type":"tool_use","id":"toolu_01roo","name":"execute_command","input":{"command":"go test ./..."}}]},
	 {"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01roo","content":"Command executed in terminal within working directory '/Users/qa/proj/widgetd'. Command execution was not successful, inspect the cause and adjust as needed.\nExit code: 1\nOutput:\n# widgetd/pkg\npkg/parser.go:42:9: undefined: frobnicateWidget\nFAIL\twidgetd/pkg [build failed]\nFAIL"},{"type":"text","text":"<environment_details>\n# Current Cost\n$0.05\n</environment_details>"}]},
	 {"role":"assistant","content":[{"type":"text","text":"frobnicateWidget was renamed to frobnicate; I'll fix the call site."},{"type":"tool_use","id":"toolu_02roo","name":"apply_diff","input":{"path":"pkg/parser.go","diff":"x"}}]}
	]`
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseRooTask(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var toolOut []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleToolOutput {
			toolOut = append(toolOut, m.Text)
		}
		if m.Role == "user" && strings.Contains(m.Text, "Exit code") {
			t.Errorf("tool output indexed as the user's words: %q", m.Text)
		}
	}
	if len(toolOut) != 1 || !strings.Contains(toolOut[0], "undefined: frobnicateWidget") {
		t.Fatalf("tool output = %q, want the failing command's output", toolOut)
	}
}

// The legacy Cline task file has the same shape and shared the same gap.
func TestClineLegacyToolResultIsIndexedAsToolOutput(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tasks", "1788845325718")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `[
	 {"role":"user","content":[{"type":"text","text":"<task>\nrun go test in widgetd\n</task>"}]},
	 {"role":"assistant","content":[{"type":"text","text":"Running."},{"type":"tool_use","id":"t1","name":"execute_command","input":{"command":"go test ./..."}}]},
	 {"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"Exit code: 1\nOutput:\npkg/parser.go:42:9: undefined: frobnicateWidget\nFAIL"}]}
	]`
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := parseClineLegacyTask(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var toolOut []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleToolOutput {
			toolOut = append(toolOut, m.Text)
		}
	}
	if len(toolOut) != 1 || !strings.Contains(toolOut[0], "undefined: frobnicateWidget") {
		t.Fatalf("tool output = %q, want the failing command's output", toolOut)
	}
}

// A build log of a few megabytes is capped like every other harness's tool
// output.
func TestRooToolResultIsCapped(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks", "1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("line of build output\n", maxParsedMessage/10)
	quoted, _ := json.Marshal(big)
	body := `[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":` + string(quoted) + `}]}]`
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseRooTask(path)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	if n := len(ss[0].Messages[0].Text); n > maxParsedMessage+64 {
		t.Errorf("tool output not capped: %d bytes", n)
	}
}
