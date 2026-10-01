package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// qwenTranscriptIn writes a one-record qwen transcript filed under the folder
// qwen names for cwd, recording cwd when record is set.
func qwenTranscriptIn(t *testing.T, root, cwd, id string, record bool) string {
	t.Helper()
	encoded := regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(cwd, "-")
	chats := filepath.Join(root, "projects", encoded, "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := map[string]any{"sessionId": id, "type": "user", "message": map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}}}
	if record {
		rec["cwd"] = cwd
	}
	line, _ := json.Marshal(rec)
	path := filepath.Join(chats, id+".jsonl")
	if err := os.WriteFile(path, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// qwen keys its sessions by the directory they ran in, so with that directory
// gone a bare `qwen -r <id>` answers "No saved session found". deja printed it
// anyway; it now refuses and names the directory and `deja show`, the way it
// does for a Cursor CLI chat (#4259).
func TestResumeQwenRefusesWhenItsDirectoryIsGone(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "qwen")
	t.Setenv("DEJA_QWEN_ROOT", root)
	gone := filepath.Join(tmp, "proj-qwen")
	path := qwenTranscriptIn(t, root, gone, "555a7310", true)

	dir, cmd, err := resumeCommand(model.Session{Harness: "qwen", ID: "555a7310", Path: path})
	if err == nil {
		t.Fatalf("printed %q (dir %q) for a session whose directory is gone", formatResumeCommand(dir, cmd), dir)
	}
	for _, want := range []string{gone, "gone", "deja show 555a7310"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not say %q", err, want)
		}
	}

	// Control: with the directory back, the same transcript resumes there.
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, cmd, err = resumeCommand(model.Session{Harness: "qwen", ID: "555a7310", Path: path})
	if err != nil || cmd != "qwen -r 555a7310" || dir != gone {
		t.Fatalf("with the directory in place: dir %q cmd %q err %v", dir, cmd, err)
	}
}

// Without a recorded cwd and with no folder on disk to read back, deja cannot
// tell where the session ran; the bare command fails the same way, so it says
// so instead of printing it.
func TestResumeQwenRefusesWhenItsDirectoryIsUnknown(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "qwen")
	t.Setenv("DEJA_QWEN_ROOT", root)
	path := qwenTranscriptIn(t, root, filepath.Join(tmp, "nowhere"), "a2d5a292", false)

	if _, cmd, err := resumeCommand(model.Session{Harness: "qwen", ID: "a2d5a292", Path: path}); err == nil {
		t.Fatalf("printed %q with no directory to run it in", cmd)
	} else if !strings.Contains(err.Error(), "deja show a2d5a292") {
		t.Errorf("refusal %q does not point at deja show", err)
	}
}

// The folder name is ambiguous: /w/my-app and /w/my/app encode the same. With
// the recorded /w/my-app gone and /w/my/app on disk, reading the folder name
// sent qwen to a directory that holds none of its sessions. A recorded cwd is
// the answer whenever there is one (#4259).
func TestResumeQwenTrustsTheRecordedDirectoryOverTheFolderName(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "qwen")
	t.Setenv("DEJA_QWEN_ROOT", root)
	gone := filepath.Join(tmp, "w", "my-app")
	if err := os.MkdirAll(filepath.Join(tmp, "w", "my", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := qwenTranscriptIn(t, root, gone, "555a7310", true)

	dir, cmd, err := resumeCommand(model.Session{Harness: "qwen", ID: "555a7310", Path: path})
	if err == nil {
		t.Fatalf("printed %q for a session whose recorded directory is gone", formatResumeCommand(dir, cmd))
	}
	if !strings.Contains(err.Error(), gone) {
		t.Errorf("refusal %q does not name the recorded directory %q", err, gone)
	}
}

// A transcript from before qwen recorded cwd still resumes from the folder
// name when that resolves on disk.
func TestResumeQwenWithoutARecordedCWDUsesTheFolder(t *testing.T) {
	// The long form: Windows hands out a temp dir under its 8.3 name
	// (RUNNER~1), which no directory listing carries, so the folder name
	// never resolved back to it.
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(tmp, "qwen")
	t.Setenv("DEJA_QWEN_ROOT", root)
	real := filepath.Join(tmp, "projects", "app")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	path := qwenTranscriptIn(t, root, real, "a2d5a292", false)

	dir, cmd, err := resumeCommand(model.Session{Harness: "qwen", ID: "a2d5a292", Path: path})
	if err != nil || cmd != "qwen -r a2d5a292" {
		t.Fatalf("an old transcript no longer resumes: %q %v", cmd, err)
	}
	if runtime.GOOS != "windows" && dir != real {
		t.Fatalf("dir = %q, want %q", dir, real)
	}
}
