package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// The MCP server told an agent the index "cannot be rebuilt" because a
// directory is not writable, and in the same breath told it to have the user
// run the command that does rebuild it. #2502 measured that: with the index
// directory read-only and its parent writable, `deja index` replaces the
// directory (#2506).
func TestTheAgentIsNotToldARebuildIsImpossibleWhenItIsNot(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	parent := t.TempDir()
	dir := filepath.Join(parent, "index.db")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	line := rebuildRefusedForAgent(dir)
	if strings.Contains(line, "cannot be rebuilt") {
		t.Errorf("the agent is told a rebuild is impossible where `deja index` does it: %q", line)
	}
	if !strings.Contains(line, "deja index") {
		t.Errorf("the agent is not told what fixes this: %q", line)
	}

	// The parent locked too: nothing can rebuild it there, and moving the
	// index is the only way out.
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	line = rebuildRefusedForAgent(dir)
	if !strings.Contains(line, "cannot be rebuilt") || !strings.Contains(line, dir) {
		t.Errorf("the agent is not told that this one really cannot be rebuilt, or which directory is at fault: %q", line)
	}
}

// The sessions reaching promotedDecisionFor are scoped by the auto activation,
// but a note carries its own project and that was never re-checked — the check
// every other note reader has (#2506).
func TestAPromotedNoteIsCheckedAgainstThePolicyToo(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	now := time.Now().UTC()
	metas := []index.SessionMeta{{ID: "a", Harness: "claude", Project: "app", Updated: now}}
	if err := sources.AppendPromoted("imported:box/app", "t", "the retry budget stays at 5",
		"claude:a", "accepted", now); err != nil {
		t.Fatal(err)
	}
	writePolicy(t, `{"activations":{"auto":{"imported":false}}}`)

	if got := promotedDecisionFor(metas); got != "" {
		t.Errorf("a note from a project the auto rule withholds reached the line before an edit: %q", got)
	}

	// The same note with the rule lifted still arrives: the check must not
	// swallow what it was not written for.
	writePolicy(t, `{}`)
	if got := promotedDecisionFor(metas); !strings.Contains(got, "retry budget stays at 5") {
		t.Errorf("the note is gone with no rule against it: %q", got)
	}
}
