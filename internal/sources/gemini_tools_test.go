package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Gemini keeps what a tool ran on the gemini record's toolCalls and what came
// back on the next user record's functionResponse; the reader read neither, so
// a Gemini store yielded no command, no tool output and no fix pair (#3293).
// Shapes from a real 2026-07 store.
func TestGeminiToolCallsBecomeCommandAndToolOutput(t *testing.T) {
	_, chats := geminiTree(t)
	lines := `{"sessionId":"sess-tools-1","projectHash":"abc","startTime":"2026-07-20T21:07:00.000Z","lastUpdated":"2026-07-20T21:08:00.000Z","kind":"main"}
{"id":"u1","timestamp":"2026-07-20T21:07:01.000Z","type":"user","content":[{"text":"run the tests"}]}
{"id":"g1","timestamp":"2026-07-20T21:07:10.000Z","type":"gemini","content":"","model":"gemini-3","toolCalls":[{"id":"run_shell_command__1","name":"run_shell_command","args":{"command":"go test ./..."},"result":[{"functionResponse":{"id":"run_shell_command__1","name":"run_shell_command","response":{"error":"Command: go test ./...\nExit Code: 1\nOutput: pkg/parser.go:42:9: undefined: frobnicateWidget\nFAIL"}}}]}]}
{"id":"u2","timestamp":"2026-07-20T21:07:12.000Z","type":"user","content":[{"functionResponse":{"id":"run_shell_command__1","name":"run_shell_command","response":{"error":"Command: go test ./...\nExit Code: 1\nOutput: pkg/parser.go:42:9: undefined: frobnicateWidget\nFAIL"}}}]}
{"id":"g2","timestamp":"2026-07-20T21:07:20.000Z","type":"gemini","content":"frobnicateWidget was renamed; fixing the call site.","model":"gemini-3"}
`
	p := filepath.Join(chats, "session-2026-07-20T21-07-sess-tools-1.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGeminiFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var cmds, tool, users []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleCommand:
			cmds = append(cmds, m.Text)
		case RoleToolOutput:
			tool = append(tool, m.Text)
		case "user":
			users = append(users, m.Text)
		}
	}
	if len(cmds) != 1 || !strings.Contains(cmds[0], "go test ./...") {
		t.Errorf("commands = %q, want the run_shell_command", cmds)
	}
	if len(tool) != 1 || !strings.Contains(tool[0], "undefined: frobnicateWidget") {
		t.Errorf("tool output = %q, want the failing command's error, once", tool)
	}
	if len(users) != 1 || users[0] != "run the tests" {
		t.Errorf("user turns = %q, want the typed prompt alone", users)
	}
}
