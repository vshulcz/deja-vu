package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The reply is attached by the recall path rather than by scoring, because
// search returns only the messages that matched — see attachAnswers in
// cmd/deja. These cover the pieces it builds on.

func TestAnswerAfterOnlyTakesTheReplyToThisTurn(t *testing.T) {
	msgs := []model.Message{
		{Role: "user", Text: "first question"},
		{Role: "user", Text: "second question before any answer"},
		{Role: "assistant", Text: "We pinned the driver."},
	}
	// The user spoke twice; the reply belongs to the second turn, not the first.
	if got := AnswerAfter(msgs, 0); got != "" {
		t.Fatalf("attached a reply across an intervening user turn: %q", got)
	}
	if got := AnswerAfter(msgs, 1); !strings.Contains(got, "pinned") {
		t.Fatalf("missed the reply that follows: %q", got)
	}
	// Nothing follows the last message.
	if got := AnswerAfter(msgs, 2); got != "" {
		t.Fatalf("invented a reply after the last message: %q", got)
	}
}

func TestDecisionTextPrefersTheOutcomeSentence(t *testing.T) {
	long := "The stack trace points at the pool. Metrics show it saturating at 40 connections. " +
		"Root cause was a pool sized from the wrong config key. We set it from the pod limit instead."
	got := DecisionText(long)
	if !strings.Contains(got, "Root cause") {
		t.Fatalf("did not pick the outcome sentence: %q", got)
	}
	// A version number is not a sentence boundary.
	if got := DecisionText("We pinned pgx to 5.4.3 and moved on."); !strings.Contains(got, "5.4.3 and moved on") {
		t.Fatalf("split on a version number: %q", got)
	}
	// With no decision language, the opening of the reply is a fair fallback.
	if got := DecisionText("Looks like the proxy closes idle connections earlier than the client expects."); !strings.HasPrefix(got, "Looks like") {
		t.Fatalf("fallback lost the opening: %q", got)
	}
	if DecisionText("   ") != "" {
		t.Fatal("whitespace produced an answer")
	}
	// Long replies are clipped on a word boundary rather than mid-token.
	got = DecisionText(strings.Repeat("word ", 200))
	if len(got) > answerCap+3 || strings.HasSuffix(got, "wo…") {
		t.Fatalf("clip landed badly: %d chars, %q", len(got), got[max(0, len(got)-20):])
	}
}
