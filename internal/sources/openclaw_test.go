package sources

import (
	"path/filepath"
	"testing"
)

func TestOpenClawSessionFiles(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "registry", "openclaw", "agents"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	files := OpenClawSessionFiles()
	if len(files) != 1 {
		t.Fatalf("files = %v, want exactly the live transcript (checkpoint and sessions.json skipped)", files)
	}
	if base := filepath.Base(files[0]); base != "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d.jsonl" {
		t.Fatalf("unexpected transcript %q", base)
	}
}

func TestParseOpenClawFile(t *testing.T) {
	path := filepath.Join("..", "..", "fixtures", "registry", "openclaw", "agents", "main", "sessions",
		"a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d.jsonl")
	sessions, err := ParseOpenClawFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	s := sessions[0]
	if s.Harness != "openclaw" {
		t.Fatalf("harness = %q", s.Harness)
	}
	if s.ID != "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d" {
		t.Fatalf("id = %q", s.ID)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(s.Messages))
	}
	if s.Messages[0].Role != "user" || s.Messages[1].Role != "assistant" {
		t.Fatalf("roles = %q,%q", s.Messages[0].Role, s.Messages[1].Role)
	}
	// Header cwd promotes to the project key.
	if s.Project != claudeProjectName(pathToProjectKey("/workspace/registry-demo")) {
		t.Fatalf("project = %q", s.Project)
	}
}

func TestOpenClawProjectFallback(t *testing.T) {
	if got := openclawProject("/x/agents/work/sessions/s.jsonl"); got != "openclaw-work" {
		t.Fatalf("project = %q, want openclaw-work", got)
	}
}
