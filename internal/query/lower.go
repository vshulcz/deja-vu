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
