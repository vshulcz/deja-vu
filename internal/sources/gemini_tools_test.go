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

// A failed run_shell_command carries its status the way a Codex or opencode
// command does (#4208). Shapes from Gemini CLI 0.60.0: the call on the gemini
// record with its result inline, and the result again on the next user record.
func TestGeminiFailedCommandCarriesItsExitCode(t *testing.T) {
	_, chats := geminiTree(t)
	lines := `{"sessionId":"sess-exit-1","projectHash":"abc","startTime":"2026-10-01T12:51:00.000Z","lastUpdated":"2026-10-01T12:51:10.000Z","kind":"main"}
{"id":"u1","timestamp":"2026-10-01T12:51:01.000Z","type":"user","content":[{"text":"show the last commit"}]}
{"id":"g1","timestamp":"2026-10-01T12:51:02.000Z","type":"gemini","content":"","model":"luna","toolCalls":[{"id":"run_shell_command__1","name":"run_shell_command","args":{"command":"git log --oneline -1","description":"log"},"result":[{"functionResponse":{"id":"run_shell_command__1","name":"run_shell_command","response":{"output":"<untrusted_context>\nOutput: fatal: your current branch 'master' does not have any commits yet\nExit Code: 128\nProcess Group PGID: 98713\n</untrusted_context>"}}}],"status":"success"},{"id":"run_shell_command__2","name":"run_shell_command","args":{"command":"git status --short"},"result":[{"functionResponse":{"id":"run_shell_command__2","name":"run_shell_command","response":{"output":"Output: (empty)\nProcess Group PGID: 98714"}}}],"status":"success"}]}
{"id":"u2","timestamp":"2026-10-01T12:51:03.000Z","type":"user","content":[{"functionResponse":{"id":"run_shell_command__1","name":"run_shell_command","response":{"output":"<untrusted_context>\nOutput: fatal: your current branch 'master' does not have any commits yet\nExit Code: 128\nProcess Group PGID: 98713\n</untrusted_context>"}}},{"functionResponse":{"id":"run_shell_command__2","name":"run_shell_command","response":{"output":"Output: (empty)\nProcess Group PGID: 98714"}}}]}
{"id":"g2","timestamp":"2026-10-01T12:51:04.000Z","type":"gemini","content":"No commits yet.","model":"luna"}
`
	p := filepath.Join(chats, "session-2026-10-01T12-51-sess-exit-1.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGeminiFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var cmds []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleCommand {
			cmds = append(cmds, m.Text)
		}
	}
	want := []string{"$ git log --oneline -1  → exit 128", "$ git status --short"}
	if strings.Join(cmds, "|") != strings.Join(want, "|") {
		t.Fatalf("commands = %q, want %q", cmds, want)
	}
}

// The status is the footer's, after the output: a command that prints an
// "Exit Code:" line of its own is not read by it, and an id Gemini reuses
// later does not carry an old command's status to a new one.
func TestGeminiExitCodeIsTheFootersOwn(t *testing.T) {
	cases := map[string]int{
		"Output: Exit Code: 3\nExit Code: 3\nok":                                                                                             0,
		"Output: building\nExit Code: 0\nfailed\nExit Code: 1\nProcess Group PGID: 9":                                                        1,
		"<untrusted_context>\nOutput: x\nExit Code: 2\nSignal: (none)\nBackground PIDs: (none)\nProcess Group PGID: 7\n</untrusted_context>": 2,
		"Output: (empty)\nProcess Group PGID: 98714":                                                                                         0,
	}
	for out, want := range cases {
		if got := geminiExitCode(out); got != want {
			t.Errorf("geminiExitCode(%q) = %d, want %d", out, got, want)
		}
	}

	_, chats := geminiTree(t)
	lines := `{"sessionId":"sess-exit-2","projectHash":"abc","startTime":"2026-10-01T12:51:00.000Z","lastUpdated":"2026-10-01T12:51:10.000Z","kind":"main"}
{"id":"g1","timestamp":"2026-10-01T12:51:02.000Z","type":"gemini","content":"","toolCalls":[{"id":"run_shell_command__1","name":"run_shell_command","args":{"command":"go test ./..."},"result":[{"functionResponse":{"id":"run_shell_command__1","name":"run_shell_command","response":{"output":"Output: ok\nProcess Group PGID: 1"}}}]}]}
{"id":"g2","timestamp":"2026-10-01T12:51:04.000Z","type":"gemini","content":"","toolCalls":[{"id":"run_shell_command__1","name":"run_shell_command","args":{"command":"ls nope"},"result":[{"functionResponse":{"id":"run_shell_command__1","name":"run_shell_command","response":{"output":"Output: ls: nope: No such file\nExit Code: 1\nProcess Group PGID: 2"}}}]}]}
`
	p := filepath.Join(chats, "session-2026-10-01T12-51-sess-exit-2.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGeminiFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	for _, m := range ss[0].Messages {
		if m.Role == RoleCommand && strings.Contains(m.Text, "go test") && strings.Contains(m.Text, "exit") {
			t.Fatalf("a reused id carried another call's status: %q", m.Text)
		}
	}
}
