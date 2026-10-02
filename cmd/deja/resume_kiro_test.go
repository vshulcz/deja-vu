package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func writeKiroFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// kiro-cli finds a session by id from anywhere, then runs it in the current
// directory and rewrites the header's cwd to it, so the command has to run
// where the session did (#4305).
func TestResumeKiroCLIRunsInTheSessionDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv("DEJA_KIRO_ROOT", root)
	proj := t.TempDir()
	id := "11111111-2222-4333-8444-555555555555"
	transcript := filepath.Join(root, "cli", id+".jsonl")
	writeKiroFile(t, filepath.Join(root, "cli", id+".json"), `{"session_id":"`+id+`","cwd":"`+filepath.ToSlash(proj)+`"}`)
	writeKiroFile(t, transcript, `{"version":"v1","kind":"Prompt","data":{"content":[{"kind":"text","data":"fix the retry loop"}]}}`+"\n")

	dir, cmd, err := resumeCommand(model.Session{Harness: "kiro", ID: id, Path: transcript})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "kiro-cli chat --resume-id "+id {
		t.Errorf("cmd = %q", cmd)
	}
	if filepath.Clean(dir) != filepath.Clean(proj) {
		t.Errorf("dir = %q, want the header's cwd %q", dir, proj)
	}

	// A directory that is gone gets no cd that would fail before kiro-cli starts.
	if err := os.RemoveAll(proj); err != nil {
		t.Fatal(err)
	}
	if dir, _, _ := resumeCommand(model.Session{Harness: "kiro", ID: id, Path: transcript}); dir != "" {
		t.Errorf("dir = %q for a directory that is gone", dir)
	}
}

// `kiro-cli --v3` writes the sess_ layout deja took for the IDE's, and lists
// the session in ~/.kiro/session-index; `kiro-cli --v3 chat --resume-id
// sess_…` continues it (#4307). One with no index entry is still the IDE's.
func TestResumeKiroV3Session(t *testing.T) {
	kiro := t.TempDir()
	root := filepath.Join(kiro, "sessions")
	t.Setenv("DEJA_KIRO_ROOT", root)
	proj := t.TempDir()
	id := "sess_2c040944-f344-45a1-bdfc-715e6f631292"
	transcript := filepath.Join(root, "f045c81011ce49e2", id, "messages.jsonl")
	writeKiroFile(t, filepath.Join(filepath.Dir(transcript), "session.json"), `{"id":"`+id+`","workspacePaths":["`+filepath.ToSlash(proj)+`"]}`)
	writeKiroFile(t, transcript, `{"timestamp":"2026-10-01T17:57:01Z","payload":{"type":"user","content":"fix the retry loop"}}`+"\n")

	s := model.Session{Harness: "kiro", ID: id, Path: transcript}
	if _, _, err := resumeCommand(s); err == nil || !strings.Contains(err.Error(), "IDE") {
		t.Fatalf("a session kiro-cli does not list: err = %v, want the IDE refusal", err)
	}

	writeKiroFile(t, filepath.Join(kiro, "session-index", "f045c81011ce49e2.jsonl"),
		`{"op":"add","sessionPath":"f045c81011ce49e2/`+id+`","at":1790877277801}`+"\n")
	dir, cmd, err := resumeCommand(s)
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "kiro-cli --v3 chat --resume-id "+id {
		t.Errorf("cmd = %q", cmd)
	}
	if filepath.Clean(dir) != filepath.Clean(proj) {
		t.Errorf("dir = %q, want the workspace %q", dir, proj)
	}
}
