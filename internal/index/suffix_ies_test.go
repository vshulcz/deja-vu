package index

import (
	"slices"
	"testing"
)

// The word-form ladder had no y <-> ies/ied step, so a consonant+y word and
// its plural could never reach each other: `deja "retry"` answered "no matches
// in 20 indexed sessions" over a store whose transcripts all say "retries"
// (#1079).
func TestSuffixFormsCrossTheYIesBoundary(t *testing.T) {
	for _, tc := range []struct {
		word string
		want []string
	}{
		{"retry", []string{"retries", "retried"}},
		{"query", []string{"queries", "queried"}},
		{"proxy", []string{"proxies", "proxied"}},
		{"retries", []string{"retry"}},
		{"queries", []string{"query"}},
		{"proxies", []string{"proxy"}},
		{"queried", []string{"query"}},
	} {
		got := suffixForms(tc.word)
		for _, want := range tc.want {
			if !slices.Contains(got, want) {
				t.Errorf("%q did not expand to %q: %v", tc.word, want, got)
			}
		}
	}
}

// A word ending in vowel+y takes a plain -s. Turning "key" into "kies" would
// spend variant slots on forms no transcript contains.
func TestSuffixFormsLeaveVowelYAlone(t *testing.T) {
	for _, word := range []string{"key", "day", "play", "boy"} {
		for _, bad := range []string{word[:len(word)-1] + "ies", word[:len(word)-1] + "ied"} {
			if slices.Contains(suffixForms(word), bad) {
				t.Errorf("%q expanded to %q; vowel+y pluralises with -s", word, bad)
			}
		}
		if !slices.Contains(suffixForms(word), word+"s") {
			t.Errorf("%q lost its plain plural", word)
		}
	}
}

func TestConsonantY(t *testing.T) {
	for _, w := range []string{"retry", "query", "proxy", "city"} {
		if base, ok := consonantY(w); !ok || base != w[:len(w)-1] {
			t.Errorf("consonantY(%q) = %q, %v", w, base, ok)
		}
	}
	for _, w := range []string{"key", "day", "boy", "guy", "y", "by", "index"} {
		if base, ok := consonantY(w); ok {
			t.Errorf("consonantY(%q) = %q, true — want false", w, base)
		}
	}
}
