package stats

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestAskedByAPersonSeparatesQuestionsFromPlumbing(t *testing.T) {
	for _, s := range []string{
		"why does the pool exhaust under load?",
		"сколько по времени займут такие прогоны?",
		"how do we handle the retry budget",
	} {
		if !AskedByAPerson(s) {
			t.Errorf("%q is a question a person asked", s)
		}
	}
	for _, s := range []string{
		"Use the deja recall tool to search for: openclaw harness",
		"Reply with only the raw JSON returned by the tool",
		"<system-reminder>something</system-reminder>",
		"The following tool was executed by the user",
		"Continue if you have next steps, or stop and ask for clarification",
		strings.Repeat("a long pasted report ", 40) + "why is it slow?",
		"",
	} {
		if AskedByAPerson(s) {
			t.Errorf("%q should not count as a question someone asked", s)
		}
	}
}

func TestRepeatQuestionsCountsOnlyRealRepeats(t *testing.T) {
	q := "why does the connection pool exhaust under load?"
	tmpl := "Use the build tool with scope all and report back"
	mk := func(id string, texts ...string) model.Session {
		s := model.Session{ID: id, Harness: "claude"}
		for _, t := range texts {
			s.Messages = append(s.Messages, model.Message{Role: "user", Text: t})
		}
		return s
	}
	ss := []model.Session{mk("a", q, tmpl), mk("b", q, tmpl), mk("c", tmpl)}
	if got := RepeatQuestions(ss); got != 1 {
		t.Fatalf("got %d, want the one question and not the template", got)
	}
}
