package search

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A long session where the query's common word is dense at the top and its
// identifying word appears only late. The window must land on the identifying
// word: that is the one that picked this session out of the index.
func TestContextWindowPrefersIdentifyingWord(t *testing.T) {
	var msgs []model.Message
	for i := range 40 {
		msgs = append(msgs, model.Message{
			Role: "user",
			Text: fmt.Sprintf("turn %d: sessions sessions sessions %s", i, strings.Repeat("filler ", 40)),
		})
	}
	msgs = append(msgs, model.Message{
		Role: "assistant",
		Text: "the prime-agent source reads PRIME_AGENT_SESSION_DIR " + strings.Repeat("body ", 40),
	})
	s := model.Session{Harness: "claude", Project: "p", ID: "id", Messages: msgs}

	var b bytes.Buffer
	PrintContext(&b, s, "prime-agent sessions")
	if !strings.Contains(b.String(), "prime-agent") {
		t.Fatalf("digest never reached the identifying word:\n%s", b.String()[:400])
	}
}

// Covering the whole query still beats covering part of it. Relaxing the old
// all-words rule must not cost the turn that says every word of the question.
// One turn saying a single query word over and over must not outweigh the turn
// that answers the whole question. Rarity alone does not settle this: repetition
// of one rare word can out-sum three of them said once each, and the two turns
// sit far enough apart that only one fits the budget.
func TestContextWindowPrefersTheTurnCoveringTheWholeQuery(t *testing.T) {
	filler := func(tag string, n int) []model.Message {
		var out []model.Message
		for i := range n {
			out = append(out, model.Message{
				Role: "user",
				Text: fmt.Sprintf("%s %d: %s", tag, i, strings.Repeat("unrelated ", 60)),
			})
		}
		return out
	}
	var msgs []model.Message
	msgs = append(msgs, model.Message{
		Role: "assistant",
		Text: "http " + strings.Repeat("http ", 50),
	})
	msgs = append(msgs, filler("mid", 40)...)
	msgs = append(msgs, model.Message{
		Role: "assistant",
		Text: "we capped retries on the http client",
	})
	msgs = append(msgs, filler("tail", 40)...)
	s := model.Session{Harness: "claude", Project: "p", ID: "id", Messages: msgs}

	var b bytes.Buffer
	PrintContext(&b, s, "http client retries")
	out := b.String()
	if !strings.Contains(out, "capped retries on the http client") {
		t.Fatalf("window sat on the loud single word instead of the answer:\n%s", out[:min(len(out), 400)])
	}
}
