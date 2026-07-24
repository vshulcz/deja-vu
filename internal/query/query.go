// Package query parses search queries and matches text against them.
// It is a leaf: index and search both build on it.
package query

import (
	"strings"
	"time"
	"unicode"
)

var stopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true,
	"at": true, "be": true, "before": true, "but": true, "by": true,
	"did": true, "do": true, "does": true, "for": true, "from": true,
	"dealt": true,
	"have":  true, "how": true, "in": true, "is": true, "it": true,
	"of": true, "on": true, "or": true, "that": true, "the": true,
	"this": true, "to": true, "was": true, "we": true, "what": true,
	"when": true, "where": true, "which": true, "who": true, "with": true,
}

// QueryParts separates ordinary terms from quoted phrases without changing
// the query syntax used by callers.
type Options struct {
	Query                     string
	Regex                     bool
	Harness, Project, Role    string
	Since                     time.Duration
	All, JSON, Fuzzy, Stemmed bool
	NoEmbed                   bool
	Semantic                  bool                `json:"-"`
	FuzzyVariants             map[string][]string `json:"-"`
	Tier                      string              `json:"-"`
	// RecallWorn maps session id -> agent recall count; filled by callers
	// from the usage log, consumed as a bounded ranking boost.
	RecallWorn map[string]int `json:"-"`
	// Now anchors relative-time phrases in the query ("a week ago"); zero
	// means the moment of the search.
	Now time.Time `json:"-"`
}

const (
	TierExact    = "exact"
	TierClose    = "close"
	TierSemantic = "semantic"
	// TierRelevance ranks sessions by IDF-weighted term overlap when the
	// exact ladder finds nothing — natural-language questions rarely survive
	// an AND over every word.
	TierRelevance = "relevance"
)

func QueryParts(q string) (terms []string, phrases []string) {
	start := -1
	var plain strings.Builder
	flushPlain := func() {
		terms = appendUnique(terms, Tokens(plain.String())...)
		plain.Reset()
	}
	for i, r := range q {
		if r != '"' {
			if start < 0 {
				plain.WriteRune(r)
			}
			continue
		}
		if start < 0 {
			flushPlain()
			start = i
			continue
		}
		content := q[start+1 : i]
		if hasLetterOrDigit(content) {
			phrases = appendUnique(phrases, strings.ToLower(strings.TrimSpace(content)))
			terms = appendUnique(terms, Tokens(content)...)
		}
		start = -1
	}
	if start >= 0 {
		// An unfinished quote is just whitespace, as it was before phrases.
		return withoutStopWords(Tokens(q)), nil
	}
	flushPlain()
	terms = withoutStopWords(terms)
	return terms, phrases
}

// IsStopWord reports whether a token is a query-time stop word. Retrieval
// key selection uses it so a long stop word like "before" cannot displace a
// short content token in the AND intersection.
func IsStopWord(term string) bool { return stopWords[term] }

func withoutStopWords(terms []string) []string {
	kept := make([]string, 0, len(terms))
	for _, term := range terms {
		if !stopWords[term] {
			kept = append(kept, term)
		}
	}
	if len(kept) == 0 {
		return terms
	}
	return kept
}

func hasLetterOrDigit(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func appendUnique(dst []string, values ...string) []string {
	seen := make(map[string]bool, len(dst)+len(values))
	for _, v := range dst {
		seen[v] = true
	}
	for _, v := range values {
		if v != "" && !seen[v] {
			dst = append(dst, v)
			seen[v] = true
		}
	}
	return dst
}

// MatchesQuery applies the text-level part of the query. Index candidates
// use this too, so phrase verification is shared by every search frontend.
func MatchesQuery(text, q string) bool {
	terms, phrases := QueryParts(q)
	return MatchesParts(text, terms, phrases, nil)
}

func MatchesParts(text string, terms, phrases []string, variants map[string][]string) bool {
	low := strings.ToLower(text)
	for _, term := range terms {
		matched := strings.Contains(low, term)
		for _, variant := range variants[term] {
			matched = matched || strings.Contains(low, variant)
		}
		if !matched {
			return false
		}
	}
	for _, phrase := range phrases {
		if !strings.Contains(low, phrase) {
			return false
		}
	}
	return len(terms) > 0 || len(phrases) > 0
}

// Tokens lowercases and dedupes whitespace-separated query tokens.
func Tokens(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, tok := range strings.Fields(strings.ToLower(s)) {
		tok = strings.Trim(tok, "\t\n\r .,;:!?()[]{}<>\"'`")
		if len(tok) < 2 || seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out
}
