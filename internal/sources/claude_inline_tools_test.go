package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseClaudeInlineToolsAndThinking(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "-workspace-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"user","entrypoint":"cli","sessionId":"inline-1","timestamp":"2026-01-02T03:04:00Z","cwd":"/workspace/app","message":{"role":"user","content":"fix handler timeout"}}`,
		`{"type":"assistant","sessionId":"inline-1","timestamp":"2026-01-02T03:04:01Z","message":{"role":"assistant","content":[{"type":"text","text":"[thinking]\nLet me inspect the handler first."},{"type":"text","text":"[tool_use Read] {\"file_path\":\"/workspace/app/pkg/handler.py\"}"},{"type":"text","text":"[tool_use Bash] {\"command\":\"pytest tests/test_handler.py\"}"}]}}`,
		`{"type":"user","entrypoint":"cli","sessionId":"inline-1","timestamp":"2026-01-02T03:04:02Z","message":{"role":"user","content":"[tool_result] def handle():\n    return 1\n"}}`,
		`{"type":"user","entrypoint":"cli","sessionId":"inline-1","timestamp":"2026-01-02T03:04:03Z","message":{"role":"user","content":"[tool_result] Exit code 1\nAssertionError: timeout"}}`,
		`{"type":"assistant","sessionId":"inline-1","timestamp":"2026-01-02T03:04:04Z","message":{"role":"assistant","content":[{"type":"text","text":"[tool_use Edit] {\"file_path\":\"/workspace/app/pkg/handler.py\",\"old_string\":\"return 1\",\"new_string\":\"return 2\"}"},{"type":"text","text":"[tool_use Write] {\"file_path\":\"/workspace/app/pkg/helper.py\",\"content\":\"def ok_helper_value(): return 42\\n\"}"},{"type":"text","text":"Fixed the timeout in handler.py."}]}}`,
		`{"type":"user","entrypoint":"cli","sessionId":"inline-1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"[tool_result] <tool_use_error>File does not exist</tool_use_error>"}}`,
		`{"type":"user","entrypoint":"cli","sessionId":"inline-1","timestamp":"2026-01-02T03:04:06Z","message":{"role":"user","content":"[tool_result] File created"}}`,
	}
	path := filepath.Join(dir, "inline-1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClaudeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	if s.ToolCalls["Read"] != 1 || s.ToolCalls["Bash"] != 1 || s.ToolCalls["Edit"] != 1 || s.ToolCalls["Write"] != 1 {
		t.Fatalf("unexpected ToolCalls: %+v", s.ToolCalls)
	}
	if s.ToolErrors != 2 {
		t.Fatalf("ToolErrors = %d, want 2 (Exit code 1 + tool_use_error)", s.ToolErrors)
	}
	if len(s.Typed) != 1 || s.Typed[0] != "fix handler timeout" {
		t.Fatalf("Typed = %q, want only human prompt", s.Typed)
	}
	var sawFiles, sawEdit, sawWrote, sawExit1 bool
	for _, m := range s.Messages {
		if m.Role == "assistant" && strings.Contains(m.Text, "inspect the handler") {
			t.Errorf("thinking leaked into assistant speech: %q", m.Text)
		}
		if m.Role == "user" && strings.Contains(m.Text, "[tool_result]") {
			t.Errorf("inline tool_result kept user role: %+v", m)
		}
		switch m.Role {
		case RoleFiles:
			if strings.Contains(m.Text, "/workspace/app/pkg/handler.py") {
				sawFiles = true
			}
		case RoleEdit:
			if strings.Contains(m.Text, "handler.py") {
				sawEdit = true
			}
		case RoleWrote:
			if strings.Contains(m.Text, "helper.py") {
				sawWrote = true
			}
		case RoleCommand:
			if strings.Contains(m.Text, "$ pytest tests/test_handler.py") && strings.Contains(m.Text, "exit 1") {
				sawExit1 = true
			}
		}
	}
	if !sawFiles || !sawEdit || !sawWrote || !sawExit1 {
		t.Fatalf("missing extracted records: files=%v edit=%v wrote=%v exit1=%v\nmsgs=%+v", sawFiles, sawEdit, sawWrote, sawExit1, s.Messages)
	}
}
