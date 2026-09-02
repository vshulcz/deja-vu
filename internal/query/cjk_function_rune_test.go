package query

import "testing"

// The index asks this rune by rune, to keep a pair of them out of the
// postings, so it is exported and has to say the same thing the bigram test
// says (#492).
func TestCJKFunctionRuneNamesTheClosedClass(t *testing.T) {
	for _, r := range []rune{'的', '了', '什', '么', '嘅'} {
		if !CJKFunctionRune(r) {
			t.Errorf("%q is in the closed class and was not named", r)
		}
	}
	for _, r := range []rune{'国', '人', '题', 'a'} {
		if CJKFunctionRune(r) {
			t.Errorf("%q carries meaning and was called grammar", r)
		}
	}
}
