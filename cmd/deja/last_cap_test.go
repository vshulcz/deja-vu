package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// manySessions writes n sessions that all mention the same topic.
func manySessions(t *testing.T, n int) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("s%d", i)
		writeClaudeFixture(t, filepath.Join(root, "-w-app", id+".jsonl"), id, []string{
			fmt.Sprintf(`{"type":"user","sessionId":"%s","cwd":"/w/app","timestamp":"2026-07-01T10:%02d:00Z","message":{"role":"user","content":"the widget pipeline keeps stalling on shard %d"}}`, id, i%60, i),
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
}

// `last` caps at ten and said nothing about the rest, while search, how, files,
// friction and log all name their cut — the misread #1632 closed for search and
// #2299 for blame (#2638).
func TestLastSaysHowManyItLeftOut(t *testing.T) {
	manySessions(t, 40)
	out, err := captureRunStderr(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "showing 10 of 40") {
		t.Fatalf("the cut is not named:\n%s", out)
	}
	if !strings.Contains(out, "deja last 40") {
		t.Fatalf("nothing says how to see the rest:\n%s", out)
	}
}

// A listing that shows everything says nothing: a line on every run is noise.
func TestLastSaysNothingWhenItShowsEverything(t *testing.T) {
	manySessions(t, 4)
	out, err := captureRunStderr(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "showing") {
		t.Fatalf("a complete listing announced a cut it did not make:\n%s", out)
	}
}

// And when the reader asked for a count of their own, the line is about that
// count rather than the default.
func TestLastNamesTheCutTheReaderAskedFor(t *testing.T) {
	manySessions(t, 40)
	out, err := captureRunStderr(t, "last", "5")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "showing 5 of 40") {
		t.Fatalf("the line does not follow the count that was asked for:\n%s", out)
	}
}
