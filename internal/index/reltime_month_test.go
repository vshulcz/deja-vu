package index

import (
	"testing"
	"time"
)

// The README offers `deja "what did we do in may"` as a feature, and the
// parser knew only relative phrases — "may" was searched as an ordinary word.
func TestMonthNamesResolveToTheMostRecentOccurrence(t *testing.T) {
	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	for query, want := range map[string]string{
		"what did we do in may": "2026-05",
		"что делали в мае":      "2026-05",
		"in june":               "2026-06",
		"back in january":       "2026-01",
		// Asking about a month later than the current one means last year's:
		// nobody is asking what they did next December.
		"in december": "2025-12",
	} {
		got := relativeTimeTerms(query, now)
		found := false
		for _, tok := range got {
			if tok == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("%q -> %v, want %s among them", query, got, want)
		}
	}
	// A query with no time in it must stay empty, or every search picks up
	// month hints it never asked for.
	if got := relativeTimeTerms("fix the parser", now); len(got) != 0 {
		t.Fatalf("time hints invented for a plain query: %v", got)
	}
	// "March" as a verb, and "may" as a modal, are the cost of this: both
	// produce a hint. The tier only ever OR-scores them, so a wrong hint
	// costs ranking weight and never filters anything out.
	if got := relativeTimeTerms("we may need to fix this", now); len(got) == 0 {
		t.Log("modal 'may' produced no hint; fine either way")
	}
}
