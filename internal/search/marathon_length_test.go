package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Ranking never sees a whole session — the index reads back the records that
// matched the query. Normalising BM25 by that made a marathon look small: it
// mentions "webhook" in two short lines out of a day's work, and those two
// lines were the entire document as far as the ranker could tell. Measured on a
// real store, one active session held first place on five of ten unrelated
// queries. The index counts the session at build time so the ranker can
// normalise by its real size.
func TestMarathonDoesNotOutrankTheSessionAboutTheQuery(t *testing.T) {
	now := time.Now()
	marathon := model.Session{
		ID: "marathon", Harness: "claude", Project: "work", Updated: now,
		// Two passing mentions, exactly what the index would hand back.
		Messages: []model.Message{
			{Role: "user", Text: "and the webhook thing too", Time: now},
			{Role: "assistant", Text: "noted the webhook, back to the parser", Time: now},
		},
		Words: 40000,
	}
	about := model.Session{
		ID: "about", Harness: "claude", Project: "work", Updated: now.Add(-time.Hour),
		Messages: []model.Message{
			{Role: "user", Text: "the webhook retries three times and then drops the event", Time: now},
			{Role: "assistant", Text: "capped webhook retries at three, the fourth attempt is dropped on purpose", Time: now},
		},
		Words: 400,
	}
	hits, err := Run([]model.Session{marathon, about}, Options{Query: "webhook"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].Session.ID != "about" {
		t.Errorf("the marathon that mentions the word outranks the session about it: %q first", hits[0].Session.ID)
	}
}

// A store indexed before the count existed reports zero words, and ranking has
// to keep working on what it can see rather than treating every session as
// weightless.
func TestRankingSurvivesASessionCountedByAnOlderIndex(t *testing.T) {
	now := time.Now()
	long := model.Session{
		ID: "long", Harness: "claude", Updated: now,
		Messages: []model.Message{
			{Role: "assistant", Text: strings.Repeat("filler ", 400) + "webhook", Time: now},
		},
	}
	short := model.Session{
		ID: "short", Harness: "claude", Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "webhook retries capped at three", Time: now},
		},
	}
	hits, err := Run([]model.Session{long, short}, Options{Query: "webhook"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].Session.ID != "short" {
		t.Errorf("without a stored word count, the padded session wins: %q first", hits[0].Session.ID)
	}
}
