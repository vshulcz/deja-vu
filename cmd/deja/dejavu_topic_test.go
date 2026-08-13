package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The déjà vu line is the one sentence a person reads when memory fires, so it
// has to name what was matched. On a long session narrowed to its matching
// region the opening line is whatever chatter began that window: seen on a real
// screen as "you have been here — \"migration locked the table\"" in answer to a
// question about token rotation.
func TestDejaVuLineNamesTheMatch(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "work-02", Project: "goprojects/deja-vu",
		Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "migration locked the table"},
			{Role: "assistant", Text: "it rewrote the whole table"},
			{Role: "user", Text: "how often does the deploy token for the billing gateway rotate?"},
			{Role: "assistant", Text: "every 47 days, pinned to the vault lease"},
		},
	}

	line := dejaVuLine(s, "deploy", "token", "rotate")
	if !strings.Contains(line, "deploy token") {
		t.Fatalf("the line names something the recall did not match:\n%s", line)
	}
	if strings.Contains(line, "migration locked") {
		t.Fatalf("the line still takes the session's opening:\n%s", line)
	}

	// With no terms there is nothing to match on, and the opening line is the
	// best summary available.
	if plain := dejaVuLine(s); !strings.Contains(plain, "migration locked") {
		t.Fatalf("the no-terms fallback changed:\n%s", plain)
	}
}
