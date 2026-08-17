package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// At session start there is no question yet, so there is nothing to match and
// the digest walks the session from the top: the first user line becomes the
// problem and the first two assistant lines become the conclusions.
//
// The first two assistant lines of a real session are "let me look" and "I have
// found the file". What the session decided is at the end. So the block an
// agent is handed before it does anything opens with the least useful sentences
// in the transcript, and the decision it exists to carry is not in it.
func TestSessionStartQuotesWhatWasConcludedNotWhatWasSaidFirst(t *testing.T) {
	s := model.Session{
		ID: "a", Harness: "claude", Project: "p", Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "the export job times out on the nightly run"},
			{Role: "assistant", Text: "Let me take a look at the job configuration."},
			{Role: "assistant", Text: "I have found the scheduler file, reading it now."},
			{Role: "user", Text: "any idea yet?"},
			{Role: "assistant", Text: "We decided to move the export off the shared pool: it now runs on its own worker with a 30 minute budget, and the timeouts stopped."},
		},
	}
	got := AutoRecallDigest([]model.Session{s}, 2000)
	if got == "" {
		t.Fatal("no digest at all for a session with a decision in it")
	}
	if !strings.Contains(got, "own worker") {
		t.Errorf("the decision this session reached is not in the block an agent is handed:\n%s", got)
	}
	if strings.Contains(got, "take a look") {
		t.Errorf("the block opens with the agent saying it will look at something:\n%s", got)
	}
}

// And when the caller does have terms — the per-prompt hook — the lines that
// carry those words are still what gets quoted. Two different questions, two
// different answers.
func TestAPromptStillQuotesTheLinesThatMatchIt(t *testing.T) {
	s := model.Session{
		ID: "b", Harness: "claude", Project: "p", Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "the export job times out on the nightly run"},
			{Role: "assistant", Text: "The scheduler holds a lock on the manifest table while it runs."},
			{Role: "assistant", Text: "We decided to move the export off the shared pool onto its own worker."},
		},
	}
	got := AutoRecallDigestFor([]model.Session{s}, 2000, []string{"manifest", "lock"})
	if !strings.Contains(got, "manifest") {
		t.Errorf("a question about the manifest lock was answered with something else:\n%s", got)
	}
}
