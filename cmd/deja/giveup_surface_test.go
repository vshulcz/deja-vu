package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A session that ended in a dead end used to arrive looking exactly like one
// that ended in an answer: the state that says otherwise is set by hand, and on
// a real store of 1160 sessions nothing carried it. The transcript says it in
// words, so the hit says it too — end to end, through indexing and search.
func TestSearchSaysWhenASessionBackedSomethingOut(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "dead-end.jsonl"), "dead-end", []string{
		`{"type":"user","sessionId":"dead-end","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"cap the webhook retries at three"}}`,
		`{"type":"assistant","sessionId":"dead-end","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":[{"type":"text","text":"capped them, then reverted the cap because the queue backed up"}]}}`,
	})
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "held.jsonl"), "held", []string{
		`{"type":"user","sessionId":"held","timestamp":"2026-01-03T03:04:05Z","message":{"role":"user","content":"what happened with the webhook queue"}}`,
		`{"type":"assistant","sessionId":"held","timestamp":"2026-01-03T03:05:05Z","message":{"role":"assistant","content":[{"type":"text","text":"the webhook queue drains fine since the batch size went up"}]}}`,
	})
	out, err := captureRun(t, "webhook")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "mentions backing an approach out") {
		t.Errorf("the session that reverted its own change is served without a word about it:\n%s", out)
	}
	// And the session that reached an answer is not labelled with someone
	// else's dead end.
	if strings.Count(out, "mentions backing an approach out") != 1 {
		t.Errorf("the marker landed on more than the session that earned it:\n%s", out)
	}
}
