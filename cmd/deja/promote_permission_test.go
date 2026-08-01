package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// promote is where a decision the user wants to keep goes; on a locked notes
// file it named a syscall and nothing to do about it, while index and forget
// both say what to change (#806).
func TestPromoteExplainsALockedNotesFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	hermeticEnv(t)
	notesDir := filepath.Join(t.TempDir(), "notes")
	if err := os.MkdirAll(notesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(notesDir, "notes.jsonl"))
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"rudder bearing clearance for acme yard"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"p8a","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "idx")
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(notesDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(notesDir, 0o700) })

	err := runPromote(dir, []string{"p8a", "--state", "accepted", "--note", "0.3mm"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("a locked notes file reported success")
	}
	msg := err.Error()
	if !strings.Contains(msg, sources.NotesFile()) {
		t.Errorf("the error does not name the file: %v", err)
	}
	if !strings.Contains(msg, "permissions") || !strings.Contains(msg, "DEJA_NOTES_FILE") {
		t.Errorf("the error does not say what to change: %v", err)
	}
	if strings.Contains(msg, "open ") {
		t.Errorf("the error still passes the syscall through: %v", err)
	}
}
