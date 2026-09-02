package query

import (
	"strings"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
)

// RelevanceTerms splits a query the way the relevance tier ranks: lowercase,
// split on anything that is not a letter or digit, expand CJK runs to bigrams,
// then drop stop words, pure grammar and anything too short to carry signal.
func RelevanceTerms(q string) []string {
	// Letters and digits are wordy in every script; everything else splits.
	// The old "anything above U+0400 is wordy" rule swallowed CJK and
	// fullwidth punctuation ("？", "，"), so a real Chinese question became
	// one giant term that matched nothing and never reached bigram
	// expansion.
	fields := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		if r < 128 {
			return (r < 'a' || r > 'z') && (r < '0' || r > '9') &&
				r != '-' && r != '_' && r != '.' && r != '/'
		}
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	fields = ExpandCJKTokens(fields)
	seen := map[string]bool{}
	var out []string
	for _, f := range fields {
		if len([]rune(f)) < 2 || (len(f) < 3 && !cjkfold.Unspaced([]rune(f)[0])) || IsStopWord(f) || seen[f] {
			continue
		}
		if CJKFunctionBigram(f) {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}
