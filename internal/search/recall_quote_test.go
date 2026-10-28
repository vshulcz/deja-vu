package search

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestQuoteCarriesTerms(t *testing.T) {
	terms := []string{"gizmoflux", "cache", "warmup"}
	digest := "- **gizmoflux cache** `abc123` · 2026-01-02\n" +
		"  - User: the gizmoflux dashboard is slow\n" +
		"  - Model said: cleared the cache on staging\n"
	if QuoteCarriesTerms(digest, terms, 2) {
		t.Error("a header holding two words, and lines holding one each, were taken as quoting the question")
	}
	if !QuoteCarriesTerms(digest, terms, 1) {
		t.Error("one word asked for, one quoted, and it was not found")
	}
	digest += "  - Model said: the gizmoflux warmup now fills the cache first\n"
	if !QuoteCarriesTerms(digest, terms, 2) {
		t.Error("a quoted line holding three words of the question was not found")
	}
	if !QuoteCarriesTerms("", terms, 0) {
		t.Error("nothing asked for must pass")
	}
}

func TestSpeechCarriesTermsTogether(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "the gizmoflux dashboard is slow"},
		{Role: "tool-output", Text: "gizmoflux cache warmup failed"},
		{Role: "assistant", Text: "cleared the cache on staging"},
	}}
	terms := []string{"gizmoflux", "cache", "warmup"}
	if SpeechCarriesTermsTogether(s, terms, 2) {
		t.Error("words together only in tool output were taken as spoken together")
	}
	if !SpeechCarriesTermsTogether(s, terms, 1) {
		t.Error("need of one is any spoken word")
	}
	if SpeechCarriesTermsTogether(s, terms[:1], 2) {
		t.Error("fewer terms than needed cannot meet")
	}
	s.Messages = append(s.Messages, model.Message{Role: "assistant", Text: "the gizmoflux cache is warm now"})
	if !SpeechCarriesTermsTogether(s, terms, 2) {
		t.Error("two words in one spoken message were missed")
	}
}
