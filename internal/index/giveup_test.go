package index

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestGiveUpLineKeepsReportsAndDropsEverythingElse(t *testing.T) {
	reports := []string{
		"reverted the retry cap, it made the queue worse",
		"откатили пул соединений, стало хуже",
		"we backed out the migration and went with the view instead",
		"the sidecar approach did not work, dropping it",
	}
	for _, l := range reports {
		if _, ok := GiveUpLine(l); !ok {
			t.Errorf("a report of backing something out is not detected: %q", l)
		}
	}
	notReports := []string{
		// A question is the moment before the decision, not the decision.
		"should we have reverted the retry cap?",
		// Source that talks about reverting is code, not a report.
		`log.Printf("reverted %s", name)`,
		"// reverted in the previous release",
		// Too short to say anything, and too long to be a statement.
		"reverted",
		// Ordinary failure talk describes the world, not a decision about it.
		"the build failed on the arm runner again",
	}
	for _, l := range notReports {
		if _, ok := GiveUpLine(l); ok {
			t.Errorf("this is not a report of backing something out: %q", l)
		}
	}
}

// Tool output is full of the word: git prints it, deja prints it, and a diff
// carries whatever the file said. Only what someone actually said counts.
func TestGaveUpReadsSpeechAndNotToolOutput(t *testing.T) {
	fromOutput := []model.Message{
		{Role: "tool-output", Text: "HEAD is now at 8f2c19a Revert \"cap retries\"\nreverted 1 commit"},
	}
	if gaveUp(fromOutput) {
		t.Error("git's own output marks the session as a dead end")
	}
	fromSpeech := []model.Message{
		{Role: "assistant", Text: "capped the retries\nreverted that, it made the queue worse"},
	}
	if !gaveUp(fromSpeech) {
		t.Error("the assistant saying it reverted the change is not detected")
	}
}
