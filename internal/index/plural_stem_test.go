package index

import (
	"slices"
	"testing"
)

// The "es" case of the suffix switch returned before the "s" case could run,
// so the plural of every noun ending in "e" reached only a stub — "pipelin",
// never "pipeline" — and the fuzzy rung answered instead, telling the reader
// their correct plural was a misspelling (#2137).
func TestAPluralReachesTheSingularOfAnEEndingNoun(t *testing.T) {
	for _, tc := range []struct{ plural, singular string }{
		{"pipelines", "pipeline"},
		{"caches", "cache"},
		{"releases", "release"},
		{"queues", "queue"},
		// The "es" strip is right for these, and stays right.
		{"boxes", "box"},
		{"matches", "match"},
		{"classes", "class"},
		// #1079's case keeps working: consonant+y goes through "ies".
		{"retries", "retry"},
		// The "ies" case had the same shape as the "es" one: a word that is
		// already "ie" plus "s" needs only the "s" gone.
		{"movies", "movie"},
		{"cookies", "cookie"},
	} {
		forms := suffixForms(tc.plural)
		if !slices.Contains(forms, tc.singular) {
			t.Errorf("suffixForms(%q) has no %q: %q", tc.plural, tc.singular, forms)
		}
		// And the catalog decides, so a store that holds the singular finds it.
		got := stemMatches(tc.plural, map[string]bool{tc.singular: true})
		if !slices.Contains(got, tc.singular) {
			t.Errorf("stemMatches(%q) = %q, want it to reach %q", tc.plural, got, tc.singular)
		}
	}
}
