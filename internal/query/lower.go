package query

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Lower lowercases s and folds ё to е. Russian writes the two letters
// interchangeably, so text and query meet on one spelling; the fold keeps the
// byte length (both letters are two bytes), so offsets found in the folded copy
// are valid in the original. Display text is never passed through here.
func Lower(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			// What strings.ToLower does past ASCII, with the fold in the same
			// pass: a second pass and a second copy for ё was a fifth of what
			// lowercasing Russian text cost (#5378).
			return strings.Map(lowerFold, s)
		}
	}
	return strings.ToLower(s)
}

func lowerFold(r rune) rune {
	r = unicode.ToLower(r)
	if r == 'ё' {
		return 'е'
	}
	return r
}

// LowerRune is Lower for one rune: Lower(s) is s with LowerRune applied to
// every rune, so a tokenizer can fold as it walks instead of copying s first.
func LowerRune(r rune) rune { return lowerFold(r) }
