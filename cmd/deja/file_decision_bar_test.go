package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A count and a decision are not the same claim, and the bar was the same for
// both. "Worked on in three sessions" is a number an agent can do nothing with,
// which is why five; a decision is the thing the same measurement showed an
// agent acts on. Counted from the manifest of a real store across every
// harness: in projects worked in more than one session, 39-62% of files have
// been touched twice against 13-36% five times, so the bar was hiding most of
// the channel behind a number nobody needed. On 120 files an agent really
// edited, the line went from speaking about 6 to speaking about 11.
func TestADecisionSpeaksBelowTheBarACountNeeds(t *testing.T) {
	dir := storeWithAFileDecidedTwice(t, 2)
	line := fileHookLine(dir, "/work/app", "/work/app/quokkabloom.go")
	if line == "" {
		t.Fatal("two sessions with a decision said nothing")
	}
	if !strings.Contains(line, "prior decision") {
		t.Errorf("the line carries no decision: %q", line)
	}
}

// And a count alone still waits for the fifth session: a file two sessions
// touched without deciding anything is ordinary work.
func TestACountAloneStillWaits(t *testing.T) {
	dir := storeWithAFileTouchedQuietly(t, 3)
	if line := fileHookLine(dir, "/work/app", "/work/app/quokkabloom.go"); line != "" {
		t.Errorf("a bare count spoke below the bar: %q", line)
	}
}

func storeWithAFileDecidedTwice(t *testing.T, n int) string {
	t.Helper()
	return storeTouchingAFile(t, n, "the retry budget for quokkabloom stays at four — decided")
}

func storeWithAFileTouchedQuietly(t *testing.T, n int) string {
	t.Helper()
	return storeTouchingAFile(t, n, "looked at quokkabloom.go again (5)")
}

// storeTouchingAFile builds a store where n sessions edited one file and ended
// with the given sentence.
func storeTouchingAFile(t *testing.T, n int, ending string) string {
	t.Helper()
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	for i := 0; i < n; i++ {
		id := "fd" + string(rune('a'+i))
		writeClaudeFixture(t, filepath.Join(claudeRoot, "app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts +
				`","message":{"role":"user","content":"look at the quokkabloom retries"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts +
				`","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit",` +
				`"input":{"file_path":"/work/app/quokkabloom.go","old_string":"a","new_string":"b"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts +
				`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"e1","content":"ok"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts +
				`","message":{"role":"assistant","content":"` + ending + `"}}`,
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}
