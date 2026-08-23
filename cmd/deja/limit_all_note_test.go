package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The truncation note pointed at --all whatever the reader had typed, so
// `--all --limit 3` answered "add --all to see the rest" — advice that changes
// nothing, since with --all on it is the limit holding the list (#1608).
func TestCappedNoteDoesNotAdviseAllWhenAllIsAlreadyOn(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("sess-%02d", i)
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/tmp/hunt","timestamp":"2026-05-0%dT10:00:00Z","message":{"role":"user","content":"the retry loop keeps firing"}}`, id, i+1)
		writeClaudeFixture(t, filepath.Join(root, "projects", "-tmp-hunt", id+".jsonl"), id, []string{line})
	}
	if err := index.Ensure(os.Getenv("DEJA_INDEX_DIR"), "", false, nil); err != nil {
		t.Fatal(err)
	}
	_ = tmp

	out, err := captureRunStderr(t, "--no-embed", "--all", "--limit", "3", "retry")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "showing 3 of 6") {
		t.Fatalf("no truncation note, so the test measured nothing:\n%s", out)
	}
	if strings.Contains(out, "add --all") {
		t.Errorf("--all is already on and the note still advises it:\n%s", out)
	}

	// The control: without --all the advice is the whole point of the line.
	out, err = captureRunStderr(t, "--no-embed", "--limit", "3", "retry")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "add --all to see the rest") {
		t.Errorf("without --all the note lost its advice:\n%s", out)
	}
}
