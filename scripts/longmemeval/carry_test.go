package main

import "testing"

// carryWords decides what counts as "the block carried the answer". Short words
// are dropped: on this dataset a gold answer holds 3.5 words of five letters or
// more, and letting three-letter words in would score "the" as a hit.
func TestCarryWordsKeepsOnlyWordsWorthMatching(t *testing.T) {
	got := carryWords("The Fujifilm X100V costs 1400 dollars")
	for _, want := range []string{"fujifilm", "x100v", "dollars"} {
		if !got[want] {
			t.Errorf("dropped %q, which is what an answer is recognised by: %v", want, got)
		}
	}
	for _, skip := range []string{"the", "1400"} {
		if got[skip] {
			t.Errorf("kept %q, which matches by accident", skip)
		}
	}
}
