package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Reasonix finds an id only in the store of the workspace it runs in, so the
// command goes with the workspace from the session's metadata. A session saved
// with no workspace is reached by its file path, which --resume also takes.
func TestResumeReasonix(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "relaylab")
	path := filepath.Join(dir, "20260920-101000.000000000-deepseek-chat.jsonl")
	meta, _ := json.Marshal(map[string]any{"workspace_root": ws})
	if err := os.WriteFile(path+".meta", meta, 0o644); err != nil {
		t.Fatal(err)
	}
	id := "20260920-101000.000000000-deepseek-chat"
	gotDir, cmd, err := resumeCommand(model.Session{Harness: "reasonix", ID: id, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if gotDir != ws || cmd != "reasonix --resume "+id {
		t.Errorf("dir/cmd = %q/%q, want the workspace and the id", gotDir, cmd)
	}

	bare := filepath.Join(dir, "bare.jsonl")
	gotDir, cmd, err = resumeCommand(model.Session{Harness: "reasonix", ID: "bare", Path: bare})
	if reasonixPathPattern.MatchString(bare) {
		if err != nil || gotDir != "" || cmd != "reasonix --resume "+bare {
			t.Errorf("no workspace: dir/cmd/err = %q/%q/%v, want the file path", gotDir, cmd, err)
		}
	}

	spaced := filepath.Join(dir, "my sessions", "bare.jsonl")
	if _, cmd, err := resumeCommand(model.Session{Harness: "reasonix", ID: "bare", Path: spaced}); err == nil {
		t.Errorf("a path with a space went on the command line unquoted: %q", cmd)
	}
}
