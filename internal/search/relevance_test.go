package search

import (
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"strings"
	"testing"
	"time"
)

func msg(text string) model.Message { return model.Message{Role: "user", Text: text} }

// The relevance tier is what answers a natural-language question when no exact
// match exists. The sessions arrive already ranked by the index — IDF-weighted
// against the whole query — so this must preserve that order rather than
// re-score by match count, which would throw away the ranking that made the
// tier worth having.
func TestRelevanceHitsKeepsTheIndexOrder(t *testing.T) {
	ss := []model.Session{
		{ID: "best", Messages: []model.Message{msg("etag reuse was replaced")}},
		{ID: "second", Messages: []model.Message{msg("etag etag etag etag everywhere")}},
		{ID: "third", Messages: []model.Message{msg("nothing to do with it")}},
	}
	hits := RelevanceHits(ss, []string{"etag"})
	if len(hits) != 3 {
		t.Fatalf("hits = %d", len(hits))
	}
	if hits[0].Session.ID != "best" || hits[1].Session.ID != "second" {
		t.Fatalf("order changed: %s, %s", hits[0].Session.ID, hits[1].Session.ID)
	}
	// The second session mentions the term far more often and must still rank
	// below the first: term frequency is not this tier's ranking signal.
	if hits[0].Score <= hits[1].Score {
		t.Fatalf("scores %v, %v do not follow the incoming order", hits[0].Score, hits[1].Score)
	}
	// Count is for display — how much of the session mentions the terms.
	if hits[0].Count != 1 || hits[2].Count != 0 {
		t.Fatalf("counts = %d, %d", hits[0].Count, hits[2].Count)
	}
	if hits[2].Tier != TierRelevance {
		t.Fatalf("tier = %v", hits[2].Tier)
	}
}

// Snippets are what the user reads; two per session is the budget, and a
// session that mentions a term in every message must not spend the whole
// screen on one result.
func TestRelevanceHitsCapsSnippets(t *testing.T) {
	msgs := make([]model.Message, 0, 10)
	for range 10 {
		msgs = append(msgs, msg("the etag check failed again"))
	}
	hits := RelevanceHits([]model.Session{{ID: "s", Messages: msgs}}, []string{"etag"})
	if len(hits[0].Snippets) > 2 {
		t.Fatalf("kept %d snippets", len(hits[0].Snippets))
	}
	if hits[0].Count != 10 {
		t.Fatalf("count = %d, should still see every mention", hits[0].Count)
	}
}

// A Simplified term should find Traditional text and the other way round: the
// same person writes both, and the index folds them together.
func TestRelevanceHitsFoldsAcrossScripts(t *testing.T) {
	hits := RelevanceHits([]model.Session{
		{ID: "trad", Messages: []model.Message{msg("修復連接池洩漏")}},
	}, []string{"修复"})
	if hits[0].Count == 0 {
		t.Fatal("a Simplified term did not match Traditional text")
	}
	if len(hits[0].Snippets) == 0 {
		t.Fatal("matched without producing a snippet to show for it")
	}
}

func TestRelativeDateReadsAsAHuman(t *testing.T) {
	now := time.Now()
	for name, tc := range map[string]struct {
		when time.Time
		want string
	}{
		"today":     {now, "today"},
		"yesterday": {now.AddDate(0, 0, -1), "1d ago"},
		"days":      {now.AddDate(0, 0, -3), "3d ago"},
	} {
		if got := RelativeDate(tc.when); got != tc.want {
			t.Fatalf("%s: RelativeDate = %q, want %q", name, got, tc.want)
		}
	}
	// Past a week it becomes a date rather than a growing day count, and past
	// a year it carries the year — "Jan 2" alone would be ambiguous.
	if got := RelativeDate(now.AddDate(0, 0, -20)); strings.HasSuffix(got, "d ago") {
		t.Fatalf("a three-week-old session reads as %q", got)
	}
	if got := RelativeDate(now.AddDate(-2, 0, 0)); !strings.Contains(got, now.AddDate(-2, 0, 0).Format("2006")) {
		t.Fatalf("a two-year-old session reads as %q, without its year", got)
	}
}

// Stop words are what keeps a query of filler from matching everything.
func TestIsStopWordCoversFillerNotIdentifiers(t *testing.T) {
	for word, want := range map[string]bool{
		"the": true, "and": true, "with": true,
		"etag": false, "deja": false, "connection": false,
	} {
		if got := IsStopWord(word); got != want {
			t.Fatalf("IsStopWord(%q) = %v, want %v", word, got, want)
		}
	}
}

// Snippet is the line the user reads under a result: it has to contain the
// term that matched, and stay short enough to scan.
func TestSnippetCentresOnTheMatch(t *testing.T) {
	long := strings.Repeat("padding text ", 40) + "the etag check failed " + strings.Repeat("more padding ", 40)
	got := Snippet(long, "etag")
	if !strings.Contains(got, "etag") {
		t.Fatalf("snippet lost the term: %q", got)
	}
	if len(got) >= len(long) {
		t.Fatalf("snippet is the whole message: %d bytes", len(got))
	}
	// A term that is not there gives the head of the message rather than
	// nothing, so a result never renders as a blank line.
	if got := Snippet("short message", "absent"); got == "" {
		t.Fatal("snippet for an absent term is empty")
	}
}

// The error tier renders the neighbourhood it found as the snippet, without
// re-scoring against the paste's words — a trace whose only shared token is a
// goroutine id would otherwise show "0 matches" over the very session that hit
// the error.
func TestErrorHitsSnippetTheNeighbourhood(t *testing.T) {
	ss := []model.Session{{
		ID: "hit", Messages: []model.Message{
			{Role: "tool-output", Text: "panic: runtime error: invalid memory address"},
			{Role: "command", Text: "added a nil guard in worker.go"},
		},
	}}
	hits := ErrorHits(ss, "")
	if len(hits) != 1 {
		t.Fatalf("hits = %d", len(hits))
	}
	if hits[0].Tier != TierError {
		t.Errorf("tier = %q", hits[0].Tier)
	}
	if hits[0].Count == 0 || len(hits[0].Snippets) == 0 {
		t.Fatalf("the neighbourhood was not snippeted: count=%d snips=%d", hits[0].Count, len(hits[0].Snippets))
	}
	joined := strings.Join(hits[0].Snippets, " ")
	if !strings.Contains(joined, "nil guard") {
		t.Errorf("the recovery is not in the snippet: %q", joined)
	}
}

// Hits are built on many cores now — lowercasing every message of every ranked
// session was the other half of a 2.4 s recall answer. The page an agent reads
// has to be the same one either way, snippets included.
func TestRelevanceHitsAreTheSameOnOneCoreAndMany(t *testing.T) {
	ss := make([]model.Session, 0, 20)
	for i := range 20 {
		ss = append(ss, model.Session{
			Harness: "claude", ID: fmt.Sprintf("s%02d", i),
			Messages: []model.Message{
				{Role: "user", Text: fmt.Sprintf("what did we settle about the retry budget in round %d", i)},
				{Role: "assistant", Text: fmt.Sprintf("capped at %d, and the scheduler stayed single-writer", i%4)},
			},
		})
	}
	terms := []string{"retry", "budget", "scheduler"}
	idf := map[string]float64{"retry": 1.5, "budget": 2, "scheduler": 3}

	relevanceWorkers = func() int { return 1 }
	one := RelevanceHitsWeighted(ss, terms, idf)
	relevanceWorkers = func() int { return 8 }
	many := RelevanceHitsWeighted(ss, terms, idf)
	t.Cleanup(func() { relevanceWorkers = defaultRelevanceWorkers })

	if len(one) != len(many) {
		t.Fatalf("one core returned %d hits, eight returned %d", len(one), len(many))
	}
	for i := range one {
		if one[i].Session.ID != many[i].Session.ID {
			t.Fatalf("position %d: one core says %s, eight say %s", i, one[i].Session.ID, many[i].Session.ID)
		}
		if one[i].Count != many[i].Count || one[i].Score != many[i].Score {
			t.Fatalf("%s scored %v/%d on one core and %v/%d on eight",
				one[i].Session.ID, one[i].Score, one[i].Count, many[i].Score, many[i].Count)
		}
		if len(one[i].Snippets) != len(many[i].Snippets) {
			t.Fatalf("%s carries %d snippets on one core and %d on eight",
				one[i].Session.ID, len(one[i].Snippets), len(many[i].Snippets))
		}
		for j := range one[i].Snippets {
			if one[i].Snippets[j] != many[i].Snippets[j] {
				t.Fatalf("%s snippet %d differs between the two paths", one[i].Session.ID, j)
			}
		}
	}
}

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
	sessions := make([]model.Session, 0, 7)
	for i := range 6 {
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
