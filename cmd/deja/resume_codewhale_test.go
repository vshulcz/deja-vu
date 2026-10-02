package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The cd goes to the workspace the session file stores, not to the project
// label derived from it, which is a relative path that only resolves from the
// workspace's parent (#4362).
func TestResumeCodeWhaleRunsInTheWorkspace(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "src", "retry-app")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "eeeeeeee-0000-4000-8000-000000000005"
	doc, _ := json.Marshal(map[string]any{
		"schema_version": 1,
		"metadata":       map[string]string{"id": id, "workspace": ws},
		"messages":       []any{},
	})
	path := filepath.Join(t.TempDir(), id+".json")
	if err := os.WriteFile(path, doc, 0o644); err != nil {
		t.Fatal(err)
	}
	s := model.Session{Harness: "codewhale", ID: id, Path: path, Project: "src/retry-app"}

	dir, cmd, err := resumeCommand(s)
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "codewhale --resume "+id {
		t.Errorf("cmd = %q", cmd)
	}
	if filepath.Clean(dir) != filepath.Clean(ws) {
		t.Errorf("dir = %q, want the session's workspace %q", dir, ws)
	}

	// A workspace that is gone gets no cd that would fail before codewhale starts.
	if err := os.RemoveAll(ws); err != nil {
		t.Fatal(err)
	}
	if dir, cmd, _ := resumeCommand(s); dir != "" || cmd != "codewhale --resume "+id {
		t.Errorf("got (%q, %q) for a workspace that is gone", dir, cmd)
	}
}
