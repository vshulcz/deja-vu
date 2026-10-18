package search

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A stem variant is the stem string, not a word of the text, so a whole-word
// comparison counted no inflected form toward BM25's term frequency while the
// verifier let the message through on the substring: the document that wrote
// the word exactly as typed won, and the ones that inflected it scored zero.
func TestCountDocumentWordsCountsEveryFormOfAStem(t *testing.T) {
	terms := []string{"лимиты"}
	variants := map[string][]string{"лимиты": {"лим"}}
	counts := make([]int, 1)
	userCounts := make([]int, 1)
	countDocumentWords("лимиты лимита лимитов лимузин лимит", terms, variants, counts, userCounts, nil, false)
	// лимиты, лимита, лимитов, лимит share the stem; лимузин only the prefix.
	if counts[0] != 4 {
		t.Fatalf("forms of one stem counted %d times, want 4", counts[0])
	}
}

// The typed word and its stem both match the same word; it is one occurrence.
func TestCountDocumentWordsCountsAFormOnce(t *testing.T) {
	terms := []string{"лимит"}
	variants := map[string][]string{"лимит": {"лимит", "лим"}}
	counts := make([]int, 1)
	countDocumentWords("лимит лимит", terms, variants, counts, make([]int, 1), nil, false)
	if counts[0] != 2 {
		t.Fatalf("two words counted %d times, want 2", counts[0])
	}
}

// English and non-stem variants count exactly as before.
func TestCountDocumentWordsEnglishUnchanged(t *testing.T) {
	terms := []string{"limit"}
	variants := map[string][]string{"limit": {"limits"}}
	counts := make([]int, 1)
	countDocumentWords("limit limits limited limiting", terms, variants, counts, make([]int, 1), nil, false)
	if counts[0] != 2 {
		t.Fatalf("english counted %d, want 2", counts[0])
	}
}

// A Russian term matched in the form the query typed earns the small surface
// bonus; another form counts toward the frequency without it, and an English
// term never gets it.
func TestSurfaceFormIsCountedForRussianTermsOnly(t *testing.T) {
	variants := map[string][]string{"лимиты": {"лимит"}, "limits": {"limit"}}
	surface := make([]int, 2)
	countDocumentWords("лимиты лимит limits limit", []string{"лимиты", "limits"}, variants, make([]int, 2), make([]int, 2), surface, false)
	if surface[0] != 1 || surface[1] != 0 {
		t.Fatalf("surface = %v, want [1 0]", surface)
	}
	now := time.Now()
	docs := []bm25Document{
		{hit: Hit{Session: model.Session{ID: "typed", Updated: now}}, termCount: []int{1}, userCount: []int{0}, surface: []int{1}, length: 10},
		{hit: Hit{Session: model.Session{ID: "form", Updated: now}}, termCount: []int{1}, userCount: []int{0}, surface: []int{0}, length: 10},
	}
	hits := scoreBM25(docs, []int{2}, 10, 10, 1, nil)
	if hits[0].Session.ID != "typed" || !(hits[0].Score > hits[1].Score) || hits[0].Score > hits[1].Score*(1+surfaceBonus)+1e-9 {
		t.Fatalf("scores = %v, %v: want the typed form ahead by at most the bonus", hits[0].Score, hits[1].Score)
	}
}
