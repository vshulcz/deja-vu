package search

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A version label printed by a failing job, a dependency bump and a pinned
// action is in the session and answers nothing about that version. Measured on
// a real store: of eight questions the per-prompt hook answered with unrelated
// work, six had every match under the role tool-output.
func TestSpeechCarriesAnyTermIgnoresToolOutput(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "прогони пайплайн ещё раз"},
		{Role: "tool-output", Text: "[toil.leader] Job 'WDLStartJob' v41 is completely failed"},
		{Role: "tool-output", Text: "uses: golangci/golangci-lint-action@v41"},
		{Role: "assistant", Text: "перезапустил, дальше смотрю логи"},
	}}
	terms := []string{"v41"}
	if !TextCarriesAnyTerm(s.Messages[1].Text, terms) {
		t.Fatal("the term is in the session; the plain test must still see it")
	}
	if SpeechCarriesAnyTerm(s, terms) {
		t.Error("only a tool printed v41, so nobody there answered anything about it")
	}
	s.Messages = append(s.Messages, model.Message{
		Role: "assistant", Text: "на v41 раскладка ломается, чинит только Ensure"})
	if !SpeechCarriesAnyTerm(s, terms) {
		t.Error("now someone said it, and that must count")
	}
}

func TestSpeechCarriesAnyTermWithoutTerms(t *testing.T) {
	s := model.Session{Messages: []model.Message{{Role: "assistant", Text: "что угодно"}}}
	if !SpeechCarriesAnyTerm(s, nil) {
		t.Error("no terms means nothing to withhold on")
	}
}
