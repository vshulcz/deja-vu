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
