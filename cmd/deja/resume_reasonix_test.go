package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Reasonix finds an id only in the store of the workspace it runs in, so the
// command goes with the workspace from the session's metadata. A session saved
// with no workspace is reached by its file path, which --resume also takes.
func TestResumeReasonix(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "relaylab")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
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

// 1.x keeps a session as a directory, and its --resume matches an id only in
// the sessions-v4 store of the working directory: a project session goes with
// the directory whose slug is its store's, and the global and desktop stores
// have no command.
func TestResumeReasonixV4(t *testing.T) {
	state := t.TempDir()
	ws := filepath.Join(t.TempDir(), "relaylab")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	slugOf := ws
	if runtime.GOOS == "windows" {
		slugOf = strings.ToLower(slugOf)
	}
	slug := strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(slugOf)
	session := func(store ...string) string {
		dir := filepath.Join(append([]string{state}, store...)...)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		m, _ := json.Marshal(map[string]any{"codec": "reasonix.session.linear/v4", "sessionId": filepath.Base(dir)})
		h, _ := json.Marshal(map[string]any{"cwd": ws})
		for name, body := range map[string][]byte{"manifest.json": m, "header.json": h, "events.frames": nil} {
			if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return filepath.Join(dir, "events.frames")
	}
	id := "839559938275e9a3ebde5a804aa24edd"
	gotDir, cmd, err := resumeCommand(model.Session{Harness: "reasonix", ID: id, Path: session("projects", slug, "sessions-v4", id)})
	if err != nil || gotDir != ws || cmd != "reasonix --resume "+id {
		t.Errorf("project: dir/cmd/err = %q/%q/%v, want the workspace and the id", gotDir, cmd, err)
	}
	// The sessions-v4 store is found only from its workspace, so with that
	// gone there is nowhere to run the command from (#4459).
	gone := filepath.Join(t.TempDir(), "gone")
	slugOf = gone
	if runtime.GOOS == "windows" {
		slugOf = strings.ToLower(slugOf)
	}
	goneSlug := strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(slugOf)
	ws = gone
	if _, cmd, err := resumeCommand(model.Session{Harness: "reasonix", ID: id, Path: session("projects", goneSlug, "sessions-v4", id)}); err == nil || !strings.Contains(err.Error(), "deja show") {
		t.Errorf("gone workspace: cmd = %q, err = %v, want a refusal pointing at deja show", cmd, err)
	}
	for _, store := range [][]string{{"sessions-v4", id}, {"desktop-sessions-v5", "by-id", "desktop-1"}} {
		if _, cmd, err := resumeCommand(model.Session{Harness: "reasonix", ID: id, Path: session(store...)}); err == nil {
			t.Errorf("%s: printed %q for a store --resume does not search", filepath.Join(store...), cmd)
		}
	}
}
