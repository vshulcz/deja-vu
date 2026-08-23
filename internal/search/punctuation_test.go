package search

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A one-word query scores on the word. The single-token fast path counted the
// raw query instead, so "retry?" — the shape of a pasted question — matched
// nothing while "retry" matched twice (#1603).
func TestOneWordQueryIgnoresPunctuation(t *testing.T) {
	ss := []model.Session{{
		ID: "a1", Harness: "claude", Project: "work/fm", Updated: time.Now(),
		Messages: []model.Message{{Role: "user", Text: "the retry backoff keeps hammering the queue"}},
	}}

	// Typographic punctuation — “ ” « » — is a separate question: `Tokens`
	// trims the ASCII set only, so those stay glued to the word and the token
	// itself is wrong before scoring ever runs. Tracked apart because the
	// index tokenizer has to agree with any change there (#1603).
	for _, q := range []string{"retry", "retry?", "retry!", "retry,", "(retry)", `"" retry`} {
		hits, err := Run(ss, Options{Query: q, Limit: 10})
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		if len(hits) != 1 {
			t.Errorf("%q found %d sessions, want 1", q, len(hits))
			continue
		}
		if hits[0].Count < 1 {
			t.Errorf("%q matched but scored %d", q, hits[0].Count)
		}
	}
}

// Membership is not enough: the same query with punctuation has to rank the
// same way. The BM25 path had its own copy of the raw-query comparison, so a
// session that says the word three times inside a hyphenated token scored zero
// and sorted below one that says it once (#1603).
func TestPunctuatedQueryRanksLikeThePlainOne(t *testing.T) {
	now := time.Now()
	ss := []model.Session{
		{ID: "hyp", Harness: "claude", Project: "p", Updated: now,
			Messages: []model.Message{{Role: "user", Text: "retry-backoff retry-backoff retry-backoff again"}}},
		{ID: "pln", Harness: "claude", Project: "p", Updated: now,
			Messages: []model.Message{{Role: "user", Text: "one plain retry here"}}},
	}

	order := func(q string) []string {
		hits, err := Run(ss, Options{Query: q, Limit: 10})
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		var ids []string
		for _, h := range hits {
			ids = append(ids, h.Session.ID)
		}
		return ids
	}

	plain := order("retry")
	if len(plain) != 2 {
		t.Fatalf("plain query found %d sessions, want 2: %v", len(plain), plain)
	}
	for _, q := range []string{"retry?", "retry!", "(retry)"} {
		if got := order(q); len(got) != len(plain) || got[0] != plain[0] || got[1] != plain[1] {
			t.Errorf("%q ranked %v, plain ranked %v", q, got, plain)
		}
	}
}
