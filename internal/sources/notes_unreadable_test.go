package sources

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Notes are read straight rather than through parseFiles, so the error was
// dropped on the floor: a notes file the process cannot read looked exactly
// like one with nothing in it, and the user's own decisions left the index
// without a word (#901).
func TestLoadNotesRecordsAFileItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not deny reads here")
	}
	dir := t.TempDir()
	notes := filepath.Join(dir, "notes.jsonl")
	if err := os.WriteFile(notes, []byte(`{"ts":"2026-08-01T10:00:00Z","project":"p","text":"a decision"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", notes)

	DiagSnapshot() // clear anything a neighbour left behind
	if ss := LoadNotes(); len(ss) != 1 {
		t.Fatalf("readable notes loaded %d sessions, want 1", len(ss))
	}
	if _, failed := DiagSnapshot(); len(failed) != 0 {
		t.Errorf("a readable file was reported as failed: %v", failed)
	}

	if err := os.Chmod(notes, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(notes, 0o600) })
	if ss := LoadNotes(); len(ss) != 0 {
		t.Fatalf("an unreadable file yielded %d sessions", len(ss))
	}
	_, failed := DiagSnapshot()
	if len(failed) != 1 || failed[notes] == "" {
		t.Errorf("the unreadable notes file was not reported: %v", failed)
	}
}
