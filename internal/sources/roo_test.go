package sources

import (
	"path/filepath"
	"testing"
)

func TestParseRooTask(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	path := filepath.Join(root, "fixtures", "registry", "roo", "tasks", "1767225700000", "api_conversation_history.json")
	ss, err := ParseRooTask(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	s := ss[0]
	if s.ID != "roo-task-1767225700000" || s.Harness != "roo" {
		t.Fatalf("identity: %+v", s)
	}
	if s.Title != "Fix the flaky test" {
		t.Fatalf("title = %q", s.Title)
	}
	if s.Project == "roo" {
		t.Fatalf("project must come from history_item workspace, got %q", s.Project)
	}
	if s.Messages[0].Text != "Fix the flaky test" {
		t.Fatalf("envelope not unwrapped: %q", s.Messages[0].Text)
	}
}

func TestRooRootsOverride(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	t.Setenv("DEJA_ROO_ROOTS", filepath.Join(root, "fixtures", "registry", "roo"))
	if files := RooTaskFiles(); len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	if ss := LoadRoo(); len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
}
