package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Three commands write the notes file — promote, `deja remember` and the MCP
// remember tool — and only promote said what to do when the write was refused.
// The other two handed back `open …: permission denied`, over MCP to an agent
// that has to decide what to tell the user (#869).
func TestEveryNoteWriterSaysWhatToDoWhenRefused(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not deny writes here")
	}
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	if err := os.WriteFile(notes, []byte(""), 0o400); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", notes)

	want := func(t *testing.T, label string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: write into a read-only notes file succeeded", label)
		}
		got := err.Error()
		if !strings.Contains(got, "cannot write "+notes) || !strings.Contains(got, "DEJA_NOTES_FILE") {
			t.Errorf("%s: %q", label, got)
		}
		if strings.HasPrefix(got, "open ") {
			t.Errorf("%s still returns the raw syscall: %q", label, got)
		}
	}

	want(t, "remember", runRemember(filepath.Join(tmp, "idx"), []string{"a decision"}))
	want(t, "mcp remember", notesWriteError(sources.AppendNote("proj", "a decision", time.Now())))
	want(t, "promoted note", notesWriteError(sources.AppendPromoted("proj", "t", "b", "claude:x", "accepted", time.Now())))

	// A writable file still writes: the helper only rewords a refusal.
	if err := os.Chmod(notes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := notesWriteError(sources.AppendNote("proj", "a decision", time.Now())); err != nil {
		t.Errorf("writable notes file refused: %v", err)
	}
}
