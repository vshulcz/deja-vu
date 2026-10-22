package search

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// longSessionAround builds a session whose query word is mentioned once in
// passing at the top, then not again until the exchange at the end that
// actually settles it, with enough conversation between the two to fill the
// context budget several times over.
func longSessionAround(turns int) model.Session {
	msgs := make([]model.Message, 0, 1+2*turns+2)
	msgs = append(msgs, model.Message{Role: "user", Text: "before we start, remind me about retries later"})
	for range turns {
		msgs = append(msgs,
			model.Message{Role: "user", Text: "unrelated question " + strings.Repeat("x", 200)},
			model.Message{Role: "assistant", Text: "unrelated answer " + strings.Repeat("y", 200)},
		)
	}
	msgs = append(msgs,
		model.Message{Role: "user", Text: "so what did we settle on for retries?"},
		model.Message{Role: "assistant", Text: "we decided to cap retries at 3 and give up after that"},
	)
	return model.Session{Harness: "claude", Project: "p", ID: "s1", Messages: msgs}
}

// The context is what gets handed to another agent, so it has to carry the
// turns that answer the query. Anchoring the budget on the first match let a
// word said once at the top pin the window there and spend all 8KB on the
// conversation that followed it.
func TestContextCarriesTheExchangeThatAnswers(t *testing.T) {
	var b bytes.Buffer
	PrintContext(&b, longSessionAround(60), "retries")
	if out := b.String(); !strings.Contains(out, "cap retries at 3") {
		t.Errorf("the turn that answers the query is missing from %d bytes of context", len(out))
	}
}

// The mirror image: the exchange that settles the question is at the top and
// the tail only mentions it in passing. A fix that simply favours the last
// match would lose the answer here instead.
func TestContextCarriesAnEarlyExchangeOverALaterMention(t *testing.T) {
	msgs := make([]model.Message, 0, 123)
	msgs = append(msgs,
		model.Message{Role: "user", Text: "what should we do about retries?"},
		model.Message{Role: "assistant", Text: "we decided to cap retries at 3 and give up after that"},
	)
	for range 60 {
		msgs = append(msgs,
			model.Message{Role: "user", Text: "unrelated question " + strings.Repeat("x", 200)},
			model.Message{Role: "assistant", Text: "unrelated answer " + strings.Repeat("y", 200)},
		)
	}
	msgs = append(msgs, model.Message{Role: "user", Text: "anyway, back to retries some other time"})

	var b bytes.Buffer
	PrintContext(&b, model.Session{Harness: "claude", Project: "p", ID: "s1", Messages: msgs}, "retries")
	if out := b.String(); !strings.Contains(out, "cap retries at 3") {
		t.Errorf("the turn that answers the query is missing from %d bytes of context", len(out))
	}
}
