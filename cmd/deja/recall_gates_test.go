package main

import (
	"github.com/vshulcz/deja-vu/internal/prompt"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func msgs(texts ...string) []model.Message {
	out := make([]model.Message, 0, len(texts))
	for _, t := range texts {
		out = append(out, model.Message{Role: "user", Text: t})
	}
	return out
}

// A long session used to be dropped whole, which excluded exactly the
// sessions people ask about — measured here, the 2% that cross the line are
// the current work.
func TestFocusSessionKeepsTheMatchingPart(t *testing.T) {
	var texts []string
	for i := 0; i < 400; i++ {
		texts = append(texts, "routine chatter with nothing to recall")
	}
	texts[200] = "we replaced the stale etag reuse with generation checks"
	s := model.Session{Messages: msgs(texts...)}
	got := focusSession(s, []string{"etag", "reuse"})
	if len(got.Messages) == 0 {
		t.Fatal("the matching part was dropped with the haystack")
	}
	if len(got.Messages) > dejaVuMaxMessages {
		t.Fatalf("kept %d messages, that is the haystack again", len(got.Messages))
	}
	found := false
	for _, m := range got.Messages {
		if strings.Contains(m.Text, "etag") {
			found = true
		}
	}
	if !found {
		t.Fatalf("narrowed to a window that misses the answer: %v", got.Messages)
	}
	// The neighbours come too: a question without its answer recalls nothing.
	if len(got.Messages) < 2 {
		t.Fatalf("kept only the matching line, no exchange: %v", got.Messages)
	}
}

// A term common enough to appear throughout keeps most of the session. Giving
// up there sends nothing; the densest windows are the answer.
func TestFocusSessionFallsBackToTheDensestWindows(t *testing.T) {
	var texts []string
	for i := 0; i < 800; i++ {
		texts = append(texts, "the queue drained again")
	}
	texts[400] = "the queue got backpressure so producers block instead of dropping"
	s := model.Session{Messages: msgs(texts...)}
	got := focusSession(s, []string{"queue", "backpressure"})
	if len(got.Messages) == 0 || len(got.Messages) > dejaVuMaxMessages {
		t.Fatalf("kept %d messages", len(got.Messages))
	}
	for _, m := range got.Messages {
		if strings.Contains(m.Text, "backpressure") {
			return
		}
	}
	t.Fatal("the one message carrying both terms was not among the densest")
}

// One informative term answers a question but must not announce a déjà vu,
// and in a small corpus even "file" clears the informativeness bar — so a
// single hit is only trusted when the question named something specific.
func TestSingleTermNeedsAnIdentifier(t *testing.T) {
	if hasIdentifierTerm([]string{"open", "file", "read"}) {
		t.Fatal("everyday words accepted as an identifier")
	}
	for _, terms := range [][]string{
		{"queue", "backpressure"},
		{"fix", "auth.go"},
		{"error", "e404"},
		{"npm_token", "rotate"},
	} {
		if !hasIdentifierTerm(terms) {
			t.Fatalf("%v has no term specific enough?", terms)
		}
	}
}

// The filler list exists because the four-character floor lets these through;
// without it "what about that thing" reads as three search terms.
func TestPromptFillerIsDropped(t *testing.T) {
	got := prompt.Terms("what about that thing we were going to make")
	if len(got) > 0 {
		t.Fatalf("pure filler produced terms: %v", got)
	}
	// And a real question still keeps its words.
	if terms := prompt.Terms("what about the etag reuse"); len(terms) == 0 {
		t.Fatal("filtering took the real terms with it")
	}
}
