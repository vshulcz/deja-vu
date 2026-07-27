package sources

import (
	"os"
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

// Roo and Cline write the same filename in the same layout. Cline's kind is
// registered first, so without a root check it claims every Roo task and the
// history is filed under the wrong harness — which is what deja did until a
// real roo CLI session showed up as cline.
func TestRooTasksAreNotClaimedByCline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_ROO_ROOTS", "")
	t.Setenv("DEJA_CLINE_ROOTS", "")
	cli := filepath.Join(home, ".vscode-mock", "global-storage")
	t.Setenv("DEJA_ROO_CLI_ROOT", cli)
	task := filepath.Join(cli, "tasks", "019fa464", "api_conversation_history.json")
	if err := os.MkdirAll(filepath.Dir(task), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(task, []byte(`[{"role":"user","content":[{"type":"text","text":"hello"}]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := KindForPath(task); got != "roo" {
		t.Fatalf("KindForPath(%s) = %q, want roo", task, got)
	}
}
