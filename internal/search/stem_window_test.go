package search

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Proximity was measured on the words as typed, so a message that held the
// query's words in another form (скрипт for скрипты) reported words that never
// meet, took no proximity boost, and a session that scattered the same words
// over a long prompt outranked the one that said them together.
func TestWindowFollowsTheFormsTheMessageUses(t *testing.T) {
	low := "r скрипт надо удалить из описи"
	toks := []string{"скрипты", "удалить", "описи"}
	variants := map[string][]string{"скрипты": {"скрипт"}}
	if w := tokenWindow(low, toks); w != 0 {
		t.Fatalf("literal window = %d, the test needs a miss to start from", w)
	}
	w := tokenWindow(low, presentTokens(low, toks, variants))
	if w <= 0 || w > 60 {
		t.Fatalf("window over the forms in the text = %d, want the width of the phrase", w)
	}
}

// Words written as typed, and any variant that is not Cyrillic, are left alone.
func TestPresentTokensLeavesEnglishAndLiteralWords(t *testing.T) {
	toks := []string{"limit", "rate"}
	got := presentTokens("rate limits", toks, map[string][]string{"limit": {"limits"}})
	if got[0] != "limit" || got[1] != "rate" {
		t.Fatalf("english tokens changed: %q", got)
	}
	got = presentTokens("лимит запросов", []string{"лимит", "запросов"}, map[string][]string{"лимит": {"лим"}})
	if got[0] != "лимит" || got[1] != "запросов" {
		t.Fatalf("literal words changed: %q", got)
	}
}

// The passage a hit quotes first is the one where the query's words come
// together, and a message that says them in another form of the word (скрипт
// for скрипты) is as tight as one that says them as typed: the tight passage
// leads, ahead of a looser one that happens to hold the typed form.
func TestSnippetLeadsWithTheTightPassageInAnotherForm(t *testing.T) {
	tight := "скрипт надо удалить из описи"
	loose := "скрипты лежат в репозитории, и когда придёт время что-то чистить, нужно будет удалить лишнее, а потом пересобрать опись, описи не трогать"
	ss := []model.Session{{
		Harness: "claude", ID: "s1", Project: "p",
		Messages: []model.Message{
			{Role: "user", Text: loose},
			{Role: "user", Text: tight},
		},
	}}
	o := Options{Query: "скрипты удалить описи", All: true, FuzzyVariants: map[string][]string{"скрипты": {"скрипт"}}}
	hits, err := runScored(ss, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || len(hits[0].quoted) < 2 {
		t.Fatalf("got %d hits, want one hit with two passages", len(hits))
	}
	if got := hits[0].quoted[0]; got != tight {
		t.Fatalf("first passage = %q, want the tight one %q", got, tight)
	}
}
