package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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

// Roo lets a user move its whole store with the roo-cline.customStoragePath
// setting. deja read only the default location, so for those users it indexed
// nothing at all and said the harness was missing.
func TestRooRootsFollowsCustomStoragePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_ROO_ROOTS", "")
	t.Setenv("DEJA_ROO_CLI_ROOT", filepath.Join(home, "absent"))
	// The settings file lives somewhere different on each platform; ask the
	// resolver rather than hardcoding the mac path, which is how this test
	// passed locally and failed on linux.
	settingsPath := vsCodeUserSettingsPaths()[0]
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(home, "relocated")
	if err := os.MkdirAll(filepath.Join(store, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Written the way VS Code writes it: comments and a trailing comma. A
	// strict decode treats this as invalid and silently finds nothing.
	settings := "{\n  // moved to an external disk\n  \"roo-cline.customStoragePath\": " +
		strconv.Quote(store) + ",\n}\n"
	if err := os.WriteFile(settingsPath, []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	roots := RooRoots()
	found := false
	for _, r := range roots {
		if r == store {
			found = true
		}
	}
	if !found {
		t.Fatalf("relocated store not in roots: %v", roots)
	}
}

func TestStripJSONCHandlesCommentsAndCommas(t *testing.T) {
	in := []byte("{\n  \"a\": \"http://x/y\", // trailing note\n  /* block */ \"b\": [1,2,],\n}")
	var got map[string]any
	if err := json.Unmarshal(dropTrailingCommas(stripJSONCComments(in)), &got); err != nil {
		t.Fatalf("jsonc not accepted: %v", err)
	}
	// A // inside a string is part of the value, not a comment.
	if got["a"] != "http://x/y" {
		t.Fatalf("a = %v", got["a"])
	}
}
