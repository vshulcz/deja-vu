package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A shown line is cut to fit, and it used to be cut from the start. On a long
// line that put the answer outside the excerpt: measured on a real store, a
// question about "hermes" recalled the session that discusses it and displayed
// a hundred and sixty characters of something else, because the word sat
// further along. The reader judges the block by that line.
func TestShownLineKeepsThePartThatMatched(t *testing.T) {
	lead := strings.Repeat("не трогай локальный git и жди завершения задачи, ", 8)
	line := lead + "дальше подними hermes на mini и проверь"

	t.Run("without terms the cut stays at the front", func(t *testing.T) {
		got := lineAround(line, 160, nil)
		if !strings.HasPrefix(got, "не трогай") {
			t.Errorf("the plain cut moved: %q", got)
		}
		if strings.Contains(got, "hermes") {
			t.Errorf("nothing asked for hermes, so nothing should have gone looking for it: %q", got)
		}
	})

	t.Run("with terms the excerpt holds the match", func(t *testing.T) {
		got := lineAround(line, 160, []string{"hermes", "mini"})
		if !strings.Contains(got, "hermes") {
			t.Errorf("the excerpt was cut before the word it matched on: %q", got)
		}
		if !strings.HasPrefix(got, "…") {
			t.Errorf("an excerpt taken from the middle has to say so: %q", got)
		}
		if r := []rune(got); len(r) > 200 {
			t.Errorf("the excerpt outgrew its budget: %d characters", len(r))
		}
		// A little of what came before, so the match reads as part of a
		// sentence rather than starting mid-thought.
		before, _, _ := strings.Cut(got, "hermes")
		if len([]rune(strings.TrimPrefix(before, "…"))) < 10 {
			t.Errorf("the excerpt begins at the match with no run-up: %q", got)
		}
	})

	t.Run("a match inside the window keeps the front", func(t *testing.T) {
		short := "подними hermes на mini " + lead
		got := lineAround(short, 160, []string{"hermes"})
		if strings.HasPrefix(got, "…") {
			t.Errorf("the excerpt moved for a match that was already visible: %q", got)
		}
	})

	t.Run("a line that fits is left alone", func(t *testing.T) {
		if got := lineAround("подними hermes", 160, []string{"hermes"}); got != "подними hermes" {
			t.Errorf("a short line was rewritten: %q", got)
		}
	})
}

// End to end, through the block the hook injects: a long question whose
// matching words sit past the cut has to be shown with them.
func TestBlockShowsTheMatchingPartOfALongQuestion(t *testing.T) {
	start := time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)
	lead := strings.Repeat("не трогай локальный git и жди завершения задачи, ", 8)
	s := model.Session{
		ID: "long-q", Harness: "claude", Project: "proj",
		Started: start, Updated: start.Add(time.Hour),
		Messages: []model.Message{
			{Role: "user", Text: lead + "дальше подними hermes на mini и проверь", Time: start},
			{Role: "assistant", Text: "поднял, работает", Time: start.Add(time.Minute)},
		},
	}
	got := AutoRecallDigestFor([]model.Session{s}, 2000, []string{"hermes", "mini"})
	if !strings.Contains(got, "hermes") {
		t.Errorf("the question line was shown without the words it matched on:\n%s", got)
	}
}
