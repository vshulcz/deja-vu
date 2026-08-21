package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func sessionSaying(text string, times int) model.Session {
	msgs := make([]model.Message, 0, times)
	for i := 0; i < times; i++ {
		msgs = append(msgs, model.Message{Role: "assistant", Text: text})
	}
	return model.Session{Harness: "claude", ID: "s", Project: "p", Messages: msgs}
}

// A single ordinary word earns an injection when the session keeps coming back
// to it. Rarity in the language cannot tell "my hamster" from a stranger's
// passing mention of one; repetition can.
func TestSessionIsAboutNeedsRepetition(t *testing.T) {
	terms := []string{"hamster"}

	once := model.Session{Messages: []model.Message{
		{Role: "assistant", Text: "someone walked past with a hamster"},
	}}
	if got := sessionIsAbout(once, terms); got != 0 {
		t.Errorf("one mention is not a subject, got %d", got)
	}

	// Literal counts, not the constant: a test written against the constant
	// stays green when the constant moves, and the number is the rule.
	if got := sessionIsAbout(sessionSaying("the hamster again", 16), terms); got != 1 {
		t.Errorf("sixteen mentions is a subject, got %d", got)
	}
	if got := sessionIsAbout(sessionSaying("the hamster again", 8), terms); got != 0 {
		t.Errorf("eight mentions is not enough to inject on one word, got %d", got)
	}

	// Case and position must not matter: the word is usually capitalised
	// somewhere and buried in a long message.
	long := strings.Repeat("filler ", 200) + strings.Repeat("Hamster ", 16)
	if got := sessionIsAbout(model.Session{Messages: []model.Message{{Text: long}}}, terms); got != 1 {
		t.Errorf("a long message repeating the word was missed, got %d", got)
	}
}
