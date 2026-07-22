package sources

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseClineModernSession(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	path := filepath.Join(root, "fixtures", "registry", "cline", "modern", "sessions", "session_synthetic_01", "session_synthetic_01.messages.json")
	ss, err := ParseClineFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	s := ss[0]
	if s.ID != "session_synthetic_01" || s.Harness != "cline" {
		t.Fatalf("identity wrong: %+v", s)
	}
	if s.Title != "Synthetic parser example" {
		t.Fatalf("title = %q", s.Title)
	}
	if s.Project == "cline" || s.Project == "" {
		t.Fatalf("project must derive from cwd, got %q", s.Project)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %d", len(s.Messages))
	}
	if s.Messages[1].Text != "Hello!" {
		t.Fatalf("thinking block must not leak: %q", s.Messages[1].Text)
	}
}

func TestParseClineModernSkipsNonLeadAgents(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "session_x")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(sess, "session_x.messages.json")
	data := `{"version":1,"agent":"subagent-3","sessionId":"session_x","messages":[{"role":"user","content":[{"type":"text","text":"internal"}],"ts":1767225600000}]}`
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClineFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 0 {
		t.Fatalf("non-lead agent transcript must be skipped, got %d sessions", len(ss))
	}
}

func TestParseClineLegacyTask(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	path := filepath.Join(root, "fixtures", "registry", "cline", "legacy", "tasks", "1767225600000", "api_conversation_history.json")
	ss, err := ParseClineFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	s := ss[0]
	if s.ID != "cline-task-1767225600000" {
		t.Fatalf("id = %q", s.ID)
	}
	if s.Title != "Say hello" {
		t.Fatalf("title from taskHistory wrong: %q", s.Title)
	}
	if s.Messages[0].Text != "Say hello" {
		t.Fatalf("task envelope must be unwrapped: %q", s.Messages[0].Text)
	}
	if s.Started.Year() != 2025 && s.Started.Year() != 2026 {
		t.Fatalf("timestamp not from taskHistory: %v", s.Started)
	}
}

func TestClineSessionFilesHonorsOverrides(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	t.Setenv("DEJA_CLINE_ROOT", filepath.Join(root, "fixtures", "registry", "cline", "modern", "sessions"))
	t.Setenv("DEJA_CLINE_ROOTS", filepath.Join(root, "fixtures", "registry", "cline", "legacy"))
	files := ClineSessionFiles()
	if len(files) != 2 {
		t.Fatalf("files = %v", files)
	}
	ss := LoadCline()
	if len(ss) != 2 {
		t.Fatalf("sessions = %d", len(ss))
	}
}
