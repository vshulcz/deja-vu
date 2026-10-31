package query

import (
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"
)

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func TestTokens(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"Hello, WORLD hello", []string{"hello", "world"}},
		{"  spaced   out  ", []string{"spaced", "out"}},
		{"a I x", nil}, // every token is a single rune, all dropped
		{"(rate) [limit].", []string{"rate", "limit"}},
		{"", nil},
		{"Dup dup DUP", []string{"dup"}}, // lowercase + dedupe
	}
	for _, c := range cases {
		got := Tokens(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Tokens(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestTokensDropsShortAndDedupes(t *testing.T) {
	got := Tokens("go go a to")
	// "a" is one rune -> dropped; "go" deduped; "to" kept (2 runes).
	if !reflect.DeepEqual(got, []string{"go", "to"}) {
		t.Fatalf("Tokens = %v", got)
	}
}

func TestQueryPartsStripsStopWords(t *testing.T) {
	terms, phrases := QueryParts("the quick brown fox")
	if len(phrases) != 0 {
		t.Fatalf("phrases = %v, want none", phrases)
	}
	want := []string{"brown", "fox", "quick"}
	if !reflect.DeepEqual(sortedCopy(terms), want) {
		t.Fatalf("terms = %v, want %v", terms, want)
	}
}

func TestQueryPartsAllStopWordsKept(t *testing.T) {
	// When every term is a stop word, withoutStopWords keeps them rather than
	// returning nothing to search for.
	terms, _ := QueryParts("the and of")
	if len(terms) == 0 {
		t.Fatal("all-stopword query returned no terms")
	}
}

func TestQueryPartsPhrase(t *testing.T) {
	terms, phrases := QueryParts(`find "rate limit" bug`)
	if !reflect.DeepEqual(phrases, []string{"rate limit"}) {
		t.Fatalf("phrases = %v", phrases)
	}
	// The phrase's own words also join the term set.
	for _, w := range []string{"find", "bug", "rate", "limit"} {
		if !contains(terms, w) {
			t.Fatalf("terms %v missing %q", terms, w)
		}
	}
}

func TestQueryPartsEmptyPhraseIgnored(t *testing.T) {
	// A quote pair with no letter or digit is not a phrase.
	_, phrases := QueryParts(`alpha "   " beta`)
	if len(phrases) != 0 {
		t.Fatalf("phrases = %v, want none", phrases)
	}
}

func TestQueryPartsUnterminatedQuote(t *testing.T) {
	// An unfinished quote falls back to plain tokenisation, no phrases.
	terms, phrases := QueryParts(`foo "bar`)
	if len(phrases) != 0 {
		t.Fatalf("phrases = %v, want none", phrases)
	}
	if !contains(terms, "foo") || !contains(terms, "bar") {
		t.Fatalf("terms = %v, want foo and bar", terms)
	}
}

func TestMatchesQuery(t *testing.T) {
	if !MatchesQuery("The rate limiter fired again", "rate limiter") {
		t.Error("expected match on both terms")
	}
	if MatchesQuery("only the rate here", "rate limiter") {
		t.Error("missing term should not match")
	}
	if MatchesQuery("anything", "") {
		t.Error("empty query should match nothing")
	}
}

func TestMatchesPartsPhrase(t *testing.T) {
	if !MatchesParts("hit the rate limit today", nil, []string{"rate limit"}, nil) {
		t.Error("phrase present should match")
	}
	if MatchesParts("rate then limit", nil, []string{"rate limit"}, nil) {
		t.Error("split phrase should not match")
	}
}

func TestMatchesPartsVariants(t *testing.T) {
	variants := map[string][]string{"colour": {"color"}}
	if !MatchesParts("the color is red", []string{"colour"}, nil, variants) {
		t.Error("variant should satisfy the term")
	}
}

func TestMatchesPartsEmptyIsFalse(t *testing.T) {
	if MatchesParts("some text", nil, nil, nil) {
		t.Error("no terms and no phrases should not match")
	}
}

func TestIsStopWord(t *testing.T) {
	for _, w := range []string{"the", "and", "что", "делай"} {
		if !IsStopWord(w) {
			t.Errorf("%q should be a stop word", w)
		}
	}
	for _, w := range []string{"rate", "limiter", "deja"} {
		if IsStopWord(w) {
			t.Errorf("%q should not be a stop word", w)
		}
	}
}

func contains(s []string, v string) bool {
	return slices.Contains(s, v)
}

func TestOptionsClockHonoursNowAndDejaNowEnv(t *testing.T) {
	explicit := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	if got := (Options{Now: explicit}).Clock(); !got.Equal(explicit) {
		t.Fatalf("explicit Now = %v, want %v", got, explicit)
	}
	t.Setenv("DEJA_NOW", "2026-10-19T08:30:00Z")
	if got := (Options{}).Clock(); !got.Equal(time.Date(2026, 10, 19, 8, 30, 0, 0, time.UTC)) {
		t.Fatalf("RFC3339 DEJA_NOW = %v", got)
	}
	t.Setenv("DEJA_NOW", "2026-10-19")
	if got := (Options{}).Clock(); !got.Equal(time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("YYYY-MM-DD DEJA_NOW = %v", got)
	}
	t.Setenv("DEJA_NOW", "1760832000")
	if got := (Options{}).Clock(); got.Unix() != 1760832000 {
		t.Fatalf("unix DEJA_NOW = %v", got)
	}
	t.Setenv("DEJA_NOW", "not-a-timestamp")
	if got := (Options{}).Clock(); got.IsZero() {
		t.Fatal("invalid DEJA_NOW returned zero time")
	}
}
