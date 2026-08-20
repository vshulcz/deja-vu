package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The session being written right now must never be recalled to itself: its
// text is already in front of the agent, and quoting it back spends tokens to
// say nothing. The hook has skipped it for a long time and nothing held that
// in place — removing the check left every test passing, which is how the
// order of the checks came to be changed without anything noticing.
func TestHookPromptDoesNotRecallTheSessionBeingWritten(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "self.jsonl"), "selfsession", []string{
		`{"type":"user","sessionId":"selfsession","timestamp":"` + old + `","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	var other bytes.Buffer
	in := strings.NewReader(`{"prompt":"do we need pgbouncer here","session_id":"another"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &other, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(other.String(), "transaction mode") {
		t.Fatalf("the fixture is not recalled at all, so the test below proves nothing:\n%q", other.String())
	}

	var self bytes.Buffer
	in = strings.NewReader(`{"prompt":"do we need pgbouncer here","session_id":"selfsession"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &self, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(self.String(), "transaction mode") {
		t.Errorf("the session being written was recalled to itself:\n%q", self.String())
	}
}
