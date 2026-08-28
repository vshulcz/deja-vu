package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// `show --json` reports the window it returned; the terminal printed the slice
// and said nothing, so a reader could not tell five turns of two hundred from a
// session that is five turns long (#2296).
func TestShowSaysWhichSliceItPrinted(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude", "project")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	var lines []string
	for i := 0; i < 20; i++ {
		at := time.Now().Add(-time.Duration(60-i) * time.Minute).UTC().Format(time.RFC3339)
		lines = append(lines, fmt.Sprintf(`{"type":"user","sessionId":"paged","timestamp":%q,"cwd":"/work/project",`+
			`"message":{"role":"user","content":"marker%02d"}}`, at, i))
	}
	if err := os.WriteFile(filepath.Join(claude, "long.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The premise: a slice really is a slice.
	out, err := captureRun(t, "show", "paged", "--harness", "claude", "--limit", "5")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "marker"); n != 5 {
		t.Fatalf("--limit 5 printed %d markers", n)
	}

	said, err := captureRunStderr(t, "show", "paged", "--harness", "claude", "--limit", "5")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(said, "5") || !strings.Contains(said, "20") {
		t.Errorf("the slice line does not say 5 of 20: %q", said)
	}

	// Past the end is its own answer, not an empty session.
	said, err = captureRunStderr(t, "show", "paged", "--harness", "claude", "--offset", "100")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(said, "past the end") {
		t.Errorf("an offset past the end printed %q", said)
	}

	// An empty session is not an offset mistake.
	if got := showWindowNote(0, 0, 0); !strings.Contains(got, "no messages") {
		t.Errorf("an empty session reads as an offset error: %q", got)
	}
	// The arithmetic at the edges.
	if got := showWindowNote(15, 5, 20); !strings.Contains(got, "16-20 of 20") {
		t.Errorf("the last slice reads %q", got)
	}
	if got := showWindowNote(0, 20, 20); got != "" {
		t.Errorf("a full window still printed a note: %q", got)
	}

	// A whole session says nothing extra.
	said, err = captureRunStderr(t, "show", "paged", "--harness", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(said, "of 20") || strings.Contains(said, "past the end") {
		t.Errorf("a whole session gained a slice line: %q", said)
	}
}
