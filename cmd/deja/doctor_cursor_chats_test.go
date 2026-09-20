package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The row exists to be read on the day Cursor switches stores, so it has to
// appear on a machine where the only Cursor files are the unread ones, and it
// has to stay quiet while every chat still has a transcript (#3772).
func TestDoctorNamesCursorChatsNothingCanRead(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CURSOR_CLI_ROOT", root)
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(root, "no-ide"))
	id := "cf555555-6666-4777-8888-999999999999"
	dir := filepath.Join(root, "chats", "ws", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "store.db"), []byte("SQLite format 3\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !doctorCursorPresent() {
		t.Fatal("doctor dropped the Cursor row on a machine that has only the unread store")
	}
	got := doctorCursorDetail(true)
	if !strings.Contains(got, "1 CLI chat with no transcript") {
		t.Fatalf("doctorCursorDetail = %q, want it to name the chat nothing can read", got)
	}

	tdir := filepath.Join(root, "projects", "-Users-me-app", "agent-transcripts", id)
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tdir, id+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := doctorCursorDetail(true); strings.Contains(got, "not readable") {
		t.Fatalf("doctorCursorDetail = %q, want silence once the transcript is there", got)
	}
}
