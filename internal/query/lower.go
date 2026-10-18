package query

import "strings"

// Lower lowercases s and folds ё to е. Russian writes the two letters
// interchangeably, so text and query meet on one spelling; the fold keeps the
// byte length (both letters are two bytes), so offsets found in the folded copy
// are valid in the original. Display text is never passed through here.
func Lower(s string) string {
	s = strings.ToLower(s)
	if !strings.Contains(s, "ё") {
		return s
	}
	return strings.ReplaceAll(s, "ё", "е")
}

// IsRussian reports whether s starts with a letter of the Russian alphabet.
// Every one of them, ё included, is two bytes led by 0xd0 or 0xd1 in UTF-8;
// Hebrew, Arabic, CJK and emoji lead with higher bytes and stay out of the
// stemmer.
func IsRussian(s string) bool {
	return len(s) >= 2 && (s[0] == 0xd0 || s[0] == 0xd1)
}
