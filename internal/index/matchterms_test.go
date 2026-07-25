package index

import "testing"

// A session can surface through the relevance tier's fold — "camped" ranking a
// session that only ever says "camping". Callers count matches and cut
// snippets by looking for the query's words in the text, so with the raw terms
// alone such a result rendered as "0 matches" with no snippet.
func TestRelevanceMatchTermsCarryFoldForms(t *testing.T) {
	got := RelevanceMatchTerms("camped beside the lake")
	first := map[string]int{}
	for i, term := range got {
		if _, seen := first[term]; !seen {
			first[term] = i
		}
	}
	if _, ok := first["camped"]; !ok {
		t.Fatalf("raw query term missing: %v", got)
	}
	if _, ok := first["camping"]; !ok {
		t.Fatalf("fold form %q missing, so a session saying it renders 0 matches: %v", "camping", got)
	}
	// Raw terms come first: callers that truncate keep the words the user typed.
	if first["camping"] < first["camped"] {
		t.Fatalf("fold forms must follow the raw terms: %v", got)
	}
	// No duplicates — the count is user-visible.
	seen := map[string]bool{}
	for _, term := range got {
		if seen[term] {
			t.Fatalf("duplicate term %q in %v", term, got)
		}
		seen[term] = true
	}
}
