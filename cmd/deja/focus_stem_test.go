package main

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A long session is narrowed to the neighbourhood of its matches before the
// block is built, and that narrowing matched exact forms only. The ranking
// reaches the session through a stem fold and the digest shows a line through
// one too, so the narrowing was the one step still spelling the word the way
// the question happened to spell it — and when it found nothing it dropped the
// session whole.
//
// Measured on a frozen copy of a real index: every block that opened on a line
// carrying none of its terms was for a session that does hold such a line.
func TestFocusKeepsAnInflectedMatch(t *testing.T) {
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	msgs := make([]model.Message, 0, dejaVuMaxMessages+4)
	for i := 0; i < dejaVuMaxMessages+2; i++ {
		at := start.Add(time.Duration(i) * time.Minute)
		msgs = append(msgs, model.Message{Role: "user", Text: "продолжай дальше", Time: at})
	}
	// The only mention, and in another case than the question asks in.
	msgs = append(msgs, model.Message{
		Role: "assistant",
		Text: "решили: индексацию перенесли в фоновый воркер после записи",
		Time: start.Add(time.Duration(len(msgs)) * time.Minute),
	})

	s := model.Session{
		ID: "focus-1", Harness: "claude", Project: "proj",
		Started: start, Updated: start.Add(time.Duration(len(msgs)) * time.Minute), Messages: msgs,
	}
	got := focusSession(s, []string{"индексация"})
	if len(got.Messages) == 0 {
		t.Fatal("the session was dropped whole because the question spelled the word in another case")
	}
	found := false
	for _, m := range got.Messages {
		if m.Text == "решили: индексацию перенесли в фоновый воркер после записи" {
			found = true
		}
	}
	if !found {
		t.Errorf("narrowing kept %d messages and threw away the only one that answers", len(got.Messages))
	}
}
