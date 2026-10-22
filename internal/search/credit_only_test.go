package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A reply that is only deja's credit line has nothing left once the credit is
// stripped. Recall printed it as an empty bullet, then quoted the credit back
// as the answer on the line under it (#4247).
func TestACreditOnlyReplyIsNeitherAnExcerptNorTheAnswer(t *testing.T) {
	credit := "déjà vu: Updated `/tmp/proj/retry.py` to return `i` inside the loop — reusing it (deja:20261001_…749_4f5443)" //nolint:gosec // a reply fixture, not a credential
	msgs := []model.Message{
		{Role: "user", Text: "what did we do about the retry loop? one line."},
		{Role: "assistant", Text: credit},
	}
	s := model.Session{Harness: "hermes", ID: "20261001_181944_e564cc", Project: "proj", Updated: time.Now(), Messages: msgs}

	hits, err := Run([]model.Session{s}, Options{Query: "retry loop", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("the session did not match")
	}
	for _, sn := range hits[0].Snippets {
		if strings.TrimSpace(sn) == "" {
			t.Errorf("an empty excerpt: %q", hits[0].Snippets)
		}
	}
	if got := AnswerAfter(msgs, 0); got != "" {
		t.Errorf("the credit line came back as the answer: %q", got)
	}

	// Control: a reply with words around the credit keeps the words.
	msgs[1].Text = "Returned i inside the loop. " + credit
	if got := AnswerAfter(msgs, 0); !strings.Contains(got, "Returned i") || strings.Contains(got, "déjà vu:") {
		t.Errorf("answer = %q, want the reply without its credit", got)
	}
}
