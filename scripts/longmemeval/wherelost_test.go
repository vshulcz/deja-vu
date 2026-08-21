package main

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestWhereLostSeparatesPackagingFromRanking(t *testing.T) {
	want := map[string]bool{"fujifilm": true}
	asked := map[string]bool{}
	spread := map[string]int{"fujifilm": 1}
	quoted := []model.Session{{Messages: []model.Message{{Text: "bought a fujifilm last spring"}}}}

	if got := whereLost(want, asked, spread, quoted); got != "packaging" {
		t.Errorf("the word sits in a session the block quoted, so the block lost it: %q", got)
	}
	if got := whereLost(want, asked, spread, nil); got != "ranking" {
		t.Errorf("the word is in the haystack but no quoted session holds it: %q", got)
	}
	if got := whereLost(want, asked, map[string]int{}, quoted); got != "absent" {
		t.Errorf("no session uses the word at all: %q", got)
	}
	// A word the question already said proves nothing about the block: it was
	// going to be there either way.
	if got := whereLost(want, map[string]bool{"fujifilm": true}, spread, quoted); got != "absent" {
		t.Errorf("a word the question itself used was counted as recalled: %q", got)
	}
	// A word the whole haystack repeats identifies nothing, so it must not
	// count as reachable material.
	if got := whereLost(want, asked, map[string]int{"fujifilm": 40}, quoted); got != "absent" {
		t.Errorf("a common word was treated as identifying: %q", got)
	}
}
