package main

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// The gate that asks whether a session's speech carries the subject was given
// one word: the rarest by IDF. Rareness is measured against the corpus, and in
// a small or homogeneous one it crowns whichever ordinary noun happens to be
// uncommon. Measured on a seeded store, "the orders service runs out of db
// connections" led on "service", so the session that settled it — which says
// "orders worker" and never "service" — was dropped here while a neighbour
// about the reporting service took its place.
func TestTheLeadIsMoreThanOneWord(t *testing.T) {
	terms := []string{"orders", "service", "runs", "db", "connections", "traffic"}
	idf := map[string]float64{
		"service": 5.54, "orders": 4.90, "traffic": 4.10,
		"connections": 2.20, "runs": 1.80, "db": 1.10,
	}
	ordered := byIdentifying(terms, idf)
	lead := ordered[:min(len(ordered), leadTermsKept)]
	if len(lead) < 2 {
		t.Fatalf("the gate still rests on one word: %v", lead)
	}

	answered := model.Session{ID: "answered", Messages: []model.Message{
		{Role: "user", Text: "the orders worker keeps dropping connections under load"},
		{Role: "assistant", Text: "Decision: default_query_exec_mode=exec on the pgx pool."},
	}}
	if !search.SpeechCarriesAnyTerm(answered, lead) {
		t.Error("the session that settled it is still dropped at the gate")
	}

	// And the gate keeps doing its job: a session that names the subject only
	// where a tool printed it is still refused.
	toolOnly := model.Session{ID: "tool-only", Messages: []model.Message{
		{Role: "user", Text: "run the suite"},
		{Role: "tool-output", Text: "orders service traffic connections all over the log"},
	}}
	if search.SpeechCarriesAnyTerm(toolOnly, lead) {
		t.Error("a session that only printed the subject passed the gate")
	}
}

// Two slots are two answers. The same content arrives from several sessions all
// the time — a split marathon, a resumed session, a workflow run again — and
// spending both on it costs the reader the second answer.
func TestTwoSlotsDoNotHoldTheSameAnswer(t *testing.T) {
	terms := []string{"reporting", "connections"}
	first := model.Session{ID: "a", Messages: []model.Message{
		{Role: "user", Text: "the reporting service is exhausting its database connections"},
		{Role: "assistant", Text: "Decision: we moved reporting to a read replica."},
	}}
	copyOf := model.Session{ID: "b", Messages: first.Messages}
	different := model.Session{ID: "c", Messages: []model.Message{
		{Role: "user", Text: "reporting connections spike after the nightly import"},
		{Role: "assistant", Text: "Decision: we throttled the import to four workers."},
	}}
	if !sameAnswerAs([]model.Session{first}, copyOf, terms) {
		t.Error("a second copy of the same answer was not recognised")
	}
	if sameAnswerAs([]model.Session{first}, different, terms) {
		t.Error("a different answer was refused as a duplicate")
	}
	if sameAnswerAs(nil, first, terms) {
		t.Error("the first session was called a duplicate of nothing")
	}
}
