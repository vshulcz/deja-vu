package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Narrowing a marathon session lowercases every message once and hands the
// result to the density pass as well. Both passes have to keep matching text
// the user typed in another case, in either alphabet.
func TestFocusSessionIgnoresCase(t *testing.T) {
	msgs := make([]model.Message, 0, 12)
	for i := 0; i < 10; i++ {
		msgs = append(msgs, model.Message{Role: "assistant", Text: "unrelated chatter"})
	}
	msgs = append(msgs,
		model.Message{Role: "assistant", Text: "PgBouncer pool size settled at 40"},
		model.Message{Role: "assistant", Text: "Индексацию перенесли в фоновый воркер"})

	got := focusSession(model.Session{Messages: msgs}, []string{"pgbouncer"})
	if len(got.Messages) == 0 {
		t.Fatal("a session mentioning the term in another case was narrowed to nothing")
	}
	var found bool
	for _, m := range got.Messages {
		if m.Text == "PgBouncer pool size settled at 40" {
			found = true
		}
	}
	if !found {
		t.Error("the matching message was dropped by the narrowing")
	}

	got = focusSession(model.Session{Messages: msgs}, []string{"индексация"})
	found = false
	for _, m := range got.Messages {
		if m.Text == "Индексацию перенесли в фоновый воркер" {
			found = true
		}
	}
	if !found {
		t.Error("the Cyrillic term matched in its own case only")
	}
}

func TestDensestMessagesUsesTheLoweredText(t *testing.T) {
	msgs := []model.Message{
		{Role: "assistant", Text: "PgBouncer and the POOL both appear here"},
		{Role: "assistant", Text: "nothing to see"},
		{Role: "assistant", Text: "pool alone"},
	}
	low := make([]string, len(msgs))
	for i, m := range msgs {
		low[i] = strings.ToLower(m.Text)
	}
	got := densestMessages(msgs, low, []string{"pgbouncer", "pool"}, 1)
	if len(got) != 1 || got[0].Text != "PgBouncer and the POOL both appear here" {
		t.Errorf("kept %v, want the message carrying both terms", got)
	}
}
