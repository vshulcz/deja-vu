package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Search ranks on recency among other things, so a session stamped in the
// future is the one it places first — and it was the only date-ordered surface
// that printed "Jan 1 2099" beside an answer and said nothing. `last`, the
// brief, doctor and MCP recall have all named it since #696 and #2104 (#3595).
func TestSearchSaysWhenAHitIsStampedAhead(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "work", "now.jsonl"), "today", []string{
		`{"type":"user","sessionId":"today","timestamp":"2026-09-13T09:00:00Z","cwd":"/work/app",` +
			`"message":{"role":"user","content":"the api will not start, checked the logs"}}`,
	})
	writeClaudeFixture(t, filepath.Join(root, "work", "ahead.jsonl"), "ahead", []string{
		`{"type":"user","sessionId":"ahead","timestamp":"2099-01-01T00:00:00Z","cwd":"/work/app",` +
			`"message":{"role":"user","content":"the api will not start, wrong clock"}}`,
	})
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", false, nil); err != nil {
		t.Fatal(err)
	}

	note, err := captureRunStderr(t, "search", "api")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "stamped later than this machine's clock") {
		t.Errorf("search printed a date that has not happened and did not say so: %q", note)
	}

	// And it stays quiet on a store where every stamp is in the past, or the
	// sentence is noise on every search anyone ever runs.
	tmp2 := hermeticEnv(t)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "work", "now.jsonl"), "past", []string{
		`{"type":"user","sessionId":"past","timestamp":"2026-09-13T09:00:00Z","cwd":"/work/app",` +
			`"message":{"role":"user","content":"the api will not start, checked the logs"}}`,
	})
	if err = index.Ensure(filepath.Join(tmp2, "index.db"), "", false, nil); err != nil {
		t.Fatal(err)
	}
	note, err = captureRunStderr(t, "search", "api")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note, "stamped later") {
		t.Errorf("search warned about a clock on a store with no future stamps: %q", note)
	}
}
