package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Qwen Code writes every tool result as a type:"tool_result" record whose
// functionResponse carries output or error; the reader kept user and
// assistant records only, so a failing command's error never reached search
// or the fix pairs (#3281). Shapes from a real 0.20.0 store.
func TestQwenToolResultIsIndexedAsToolOutput(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_QWEN_ROOT", root)
	dir := filepath.Join(root, "projects", "-w-app", "chats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rows := []string{
		`{"sessionId":"s1","timestamp":"2026-09-08T06:21:00.000Z","type":"user","cwd":"/w/app","message":{"role":"user","parts":[{"text":"build the missing package"}]}}`,
		`{"sessionId":"s1","timestamp":"2026-09-08T06:21:10.000Z","type":"assistant","cwd":"/w/app","message":{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"run_shell_command","args":{"command":"go build ./missingpkg"}}}]}}`,
		`{"sessionId":"s1","timestamp":"2026-09-08T06:21:22.504Z","type":"tool_result","cwd":"/w/app","message":{"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"run_shell_command","response":{"error":"Command: go build ./missingpkg\nDirectory: (root)\nOutput: go: go.mod file not found in current directory or any parent directory; see 'go help modules'\nError: (none)\nExit Code: 1"}}}]},"toolCallResult":{"callId":"call_1","status":"error"}}`,
		`{"sessionId":"s1","timestamp":"2026-09-08T06:21:30.000Z","type":"tool_result","cwd":"/w/app","message":{"role":"user","parts":[{"functionResponse":{"id":"call_2","name":"agent","response":{"output":"Cannot use working_dir: /w is not a git repository."}}}]},"toolCallResult":{"callId":"call_2","status":"success"}}`,
		`{"sessionId":"s1","timestamp":"2026-09-08T06:21:40.000Z","type":"assistant","cwd":"/w/app","message":{"role":"model","parts":[{"text":"go.mod is missing; I will init the module."}]}}`,
	}
	path := filepath.Join(dir, "s1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseQwenFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var toolOut []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleToolOutput {
			toolOut = append(toolOut, m.Text)
		}
		if m.Role == "user" && strings.Contains(m.Text, "go.mod file not found") {
			t.Errorf("tool output indexed as the user's words: %q", m.Text)
		}
	}
	if len(toolOut) != 2 || !strings.Contains(toolOut[0], "go.mod file not found") || !strings.Contains(toolOut[1], "not a git repository") {
		t.Fatalf("tool output = %q, want the error and the output of the two results", toolOut)
	}
}
