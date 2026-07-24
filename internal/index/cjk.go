package index

import (
	"unicode"
)

// CJK text carries no spaces, so the base tokenizer collapses whole phrases
// into one giant token and the exact tier never fires for it. The classic
// dictionary-free fix (Lucene's CJKAnalyzer family, proposed in #337) indexes
// overlapping bigrams inside every CJK run: 装订计数 -> 装订, 订计, 计数.
// A single-rune run keeps its unigram. Runs never cross a non-CJK boundary,
// so ASCII and Cyrillic paths are untouched, and bigrams are ordinary tokens
// downstream — postings, ranking and the ladder apply unchanged.

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r)
}

// cjkBigrams emits the bigram set for every CJK run in s.
func cjkBigrams(s string) []string {
	var out []string
	seen := map[string]bool{}
	var run []rune
	flush := func() {
		switch {
		case len(run) == 1:
			t := string(run)
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		case len(run) >= 2:
			for i := 0; i+1 < len(run); i++ {
				t := string(run[i : i+2])
				if !seen[t] {
					seen[t] = true
					out = append(out, t)
				}
			}
		}
		run = run[:0]
	}
	for _, r := range s {
		if isCJK(r) {
			run = append(run, r)
			continue
		}
		flush()
	}
	flush()
	return out
}

// expandCJKTokens maps each token to itself or, when the token is a pure CJK
// run of 2+ runes, to its bigram set — the query-side mirror of the index
// emitter, so multi-character queries AND their bigrams.
func expandCJKTokens(toks []string) []string {
	out := make([]string, 0, len(toks))
	for _, t := range toks {
		runes := []rune(t)
		pure := len(runes) >= 2
		for _, r := range runes {
			if !isCJK(r) {
				pure = false
				break
			}
		}
		if !pure {
			out = append(out, t)
			continue
		}
		out = append(out, cjkBigrams(t)...)
	}
	return out
}
