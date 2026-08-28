package search

import "testing"

func TestRecallWorthShowing(t *testing.T) {
	if RecallWorthShowing([]string{"pgbouncer"}, 0, -1) {
		t.Error("nothing matched, so there is nothing to show")
	}
	if !RecallWorthShowing([]string{"pgbouncer"}, 1, -1) {
		t.Error("a word that identifies something stands on one match")
	}
	if RecallWorthShowing([]string{"build"}, 1, -1) {
		t.Error("one ordinary word is not a subject on its own")
	}
	if !RecallWorthShowing([]string{"build", "retry"}, 2, -1) {
		t.Error("two ordinary words that both matched earn the block")
	}
}

func TestHasIdentifierTerm(t *testing.T) {
	for _, term := range []string{"pgbouncer", "omoda", "сеть", "v11.2", "pkg/index", "bot_id"} {
		if !HasIdentifierTerm([]string{term}) {
			t.Errorf("%q names something", term)
		}
	}
	for _, term := range []string{"build", "tests", "line", "of", ""} {
		if HasIdentifierTerm([]string{term}) {
			t.Errorf("%q is ordinary vocabulary", term)
		}
	}
}
