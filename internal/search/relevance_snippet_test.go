package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The relevance ranker scores a session by the idf mass of its best single
// message: the passage where the rare words of the question are is what makes
// the session an answer. It then throws that away, and the snippet is chosen by
// a different rule — how many distinct query terms a message contains, each
// counted the same.
//
// The two disagree exactly when one rare word carries the session. "How many
// bikes do I own?" ranks on many, bikes, own; a message saying "many people own
// one" holds two of them and the message saying "three bikes" holds one, so the
// session is ranked on the second and shown as the first.
//
// That is the whole of what an agent reads before deciding whether to use a
// result, which is why it is worth its own test rather than an aggregate.
func TestTheSnippetComesFromTheMessageThatEarnedTheRank(t *testing.T) {
	// The filler words have to be common across the returned set for this to be
	// the real situation; a term is only worth little because everything has it.
	var sessions []model.Session
	for i := 0; i < 6; i++ {
		sessions = append(sessions, model.Session{
			ID: string(rune('a' + i)), Harness: "claude", Project: "p",
			Messages: []model.Message{
				{Role: "user", Text: "there are many ways to own one of these"},
			},
		})
	}
	answer := model.Session{
		ID: "answer", Harness: "claude", Project: "p",
		Messages: []model.Message{
			{Role: "user", Text: "how many of these do people own, generally speaking"},
			{Role: "user", Text: "I keep three bikes in the garage and ride the blue one"},
		},
	}
	sessions = append(sessions, answer)

	hits := RelevanceHits(sessions, []string{"many", "bikes", "own"})
	var got *Hit
	for i := range hits {
		if hits[i].Session.ID == "answer" {
			got = &hits[i]
		}
	}
	if got == nil {
		t.Fatal("the session holding the rare word is not in the hits at all")
	}
	if len(got.Snippets) == 0 {
		t.Fatal("no snippet at all for the session that answers the question")
	}
	if !strings.Contains(strings.ToLower(got.Snippets[0]), "bikes") {
		t.Errorf("the first snippet is the filler message, not the one that earned the rank:\n  %s", got.Snippets[0])
	}
}
