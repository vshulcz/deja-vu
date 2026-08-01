package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The borrowed title is the forgotten session's first turn — a customer name
// in the case that found this (#666, #690). Dropping the rewrite error left
// three success lines standing over a line still on disk (#804).
func TestForgetSaysWhenTheBorrowedTitleSurvived(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	hermeticEnv(t)
	// Its own directory: hermeticEnv puts notes.jsonl beside HOME, and locking
	// that would lock the whole fixture rather than the notes file.
	notesDir := filepath.Join(t.TempDir(), "notes")
	if err := os.MkdirAll(notesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(notesDir, "notes.jsonl"))
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"gearbox oil change for acme yard"},"timestamp":"2026-07-03T10:00:00Z","sessionId":"p7c","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "idx")
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "promote", "p7c", "--state", "accepted", "--note", "80W90"); err != nil {
		t.Fatal(err)
	}
	notes := sources.NotesFile()
	before, err := os.ReadFile(notes)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), "acme yard") {
		t.Fatalf("the note did not borrow the title:\n%s", before)
	}

	locked := filepath.Dir(notes)
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	out, err := captureRun(t, "forget", "--session", "p7c")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "could not clear the title") {
		t.Errorf("a failed rewrite was reported as success:\n%s", out)
	}
	// The error text already contains the temp path, so asserting on the file
	// name alone passes without the advice line.
	if !strings.Contains(out, "it is still in "+notes) {
		t.Errorf("the output does not say where the borrowed title still is:\n%s", out)
	}
	if !strings.Contains(out, "run this again") {
		t.Errorf("the output does not say what to do:\n%s", out)
	}
	if strings.Contains(out, "cleared the borrowed title") {
		t.Errorf("output claims the title was cleared:\n%s", out)
	}
	after, err := os.ReadFile(notes)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "acme yard") {
		t.Fatal("fixture no longer reproduces the failure")
	}
	_ = dir
}
