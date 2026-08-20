package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestNearCopyTellsARepeatedInstructionFromAnAnswer(t *testing.T) {
	asked := "да, начинай с ретрая на воркере"
	for _, line := range []string{
		"да, начинай с ретрая на воркере",
		"да, начинай с ретрая на воркере 2",
		"начинай с ретрая на воркере",
	} {
		if !nearCopy(line, asked) {
			t.Errorf("a repeat of the message being typed is not recognised:\n  %q", line)
		}
	}
	for _, line := range []string{
		"ретрай на воркере теперь ограничен четырьмя попытками, дальше очередь",
		"retries on the worker are capped at four",
		"",
	} {
		if nearCopy(line, asked) {
			t.Errorf("an answer is mistaken for a repeat of the question:\n  %q", line)
		}
	}
	if nearCopy("что угодно", "") {
		t.Error("with nothing asked there is nothing to compare against")
	}
}

// The digest must not open by handing back the sentence being typed: the
// session holding an earlier copy of it carries every query word, so it wins
// the opening slot on similarity alone.
func TestDigestDoesNotOpenWithTheMessageBeingTyped(t *testing.T) {
	now := time.Now().Add(-48 * time.Hour)
	s := model.Session{
		ID: "s1", Harness: "claude", Project: "p", Started: now, Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "start the cormorant retry now", Time: now},
			{Role: "assistant", Text: "the fix: cormorant retries are capped at four", Time: now.Add(time.Minute)},
		},
	}
	terms := []string{"cormorant", "retry", "start"}

	plain := AutoRecallDigestFor([]model.Session{s}, 2000, terms)
	if !strings.Contains(plain, "start the cormorant retry now") {
		t.Fatalf("without the asked text the echo is expected to win, so this test proves nothing:\n%s", plain)
	}

	asked := AutoRecallDigestForAsked([]model.Session{s}, 2000, terms, "start the cormorant retry now")
	if strings.Contains(asked, "start the cormorant retry now") {
		t.Errorf("the block hands back the sentence being typed:\n%s", asked)
	}
	if !strings.Contains(asked, "capped at four") {
		t.Errorf("the conclusion is not shown:\n%s", asked)
	}
}
