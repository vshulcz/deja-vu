package search

import (
	"strings"
	"unicode"
)

// QueryParts separates ordinary terms from quoted phrases without changing
// the query syntax used by callers.
func QueryParts(q string) (terms []string, phrases []string) {
	start := -1
	var plain strings.Builder
	flushPlain := func() {
		terms = appendUnique(terms, queryTokens(plain.String())...)
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
			terms = appendUnique(terms, queryTokens(content)...)
		}
		start = -1
	}
	if start >= 0 {
		// An unfinished quote is just whitespace, as it was before phrases.
		return queryTokens(q), nil
	}
	flushPlain()
	return terms, phrases
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
