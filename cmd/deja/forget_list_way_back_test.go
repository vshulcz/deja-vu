package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The list is where someone who dropped more than they meant to lands, and it
// named no way back: `--unforget` was in `deja help` only, and the hint for a
// guessed `deja unforget` pointed at this same list (#919).
func TestForgetListNamesTheWayBack(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-10T10:00:00Z","sessionId":"ab12-one","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "forget", "--session", "ab12"); err != nil {
		t.Fatal(err)
	}

	// The ids stay on stdout so the list is still pipeable; the way back is a
	// note beside it.
	ids, err := captureRun(t, "forget", "--list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ids, "--unforget") {
		t.Errorf("the id list is no longer just ids:\n%s", ids)
	}
	note, err := captureRunStderr(t, "forget", "--list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "--unforget") {
		t.Errorf("the list does not name the way back: %q", note)
	}

	// Nothing forgotten, nothing to say.
	if _, err := captureRunStderr(t, "forget", "--unforget", "claude:ab12-one"); err != nil {
		t.Fatal(err)
	}
	if note, err := captureRunStderr(t, "forget", "--list"); err != nil {
		t.Fatal(err)
	} else if strings.Contains(note, "--unforget") {
		t.Errorf("an empty list still advises an undo: %q", note)
	}

	// And the wrong guess with a real answer behind it names that answer
	// rather than the list alone.
	if got := commandHint("unforget"); !strings.Contains(got, "forget --unforget") {
		t.Errorf("the guess hint does not name the command: %q", got)
	}
}

// An empty list printed nothing on either stream, which reads the same as a
// command that did not run — and this is the command the other refusals point
// at (#1040). The line goes to stderr so a pipe still counts only ids.
func TestAnEmptyForgetListSaysSo(t *testing.T) {
	hermeticEnv(t)
	out, err := captureRun(t, "forget", "--list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("stdout is not empty, a pipe would count it: %q", out)
	}
	errOut, err := captureRunStderr(t, "forget", "--list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "nothing is forgotten on this machine") {
		t.Errorf("an empty list says nothing at all: %q", errOut)
	}
}
