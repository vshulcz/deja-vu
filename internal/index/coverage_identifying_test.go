package index

import "testing"

// Coverage multiplies a session's score by how much of the query it covers, and
// it used to count every word the informative gate let through. That gate is
// deliberately generous: it decides whether a term is worth speaking about at
// all, and takes the more forgiving of its two readings of "rare" so a subject
// word is never dismissed as filler. The two readings come apart on the shape a
// real store is full of — a word sitting in one message of each of many long
// sessions is ordinary counted in sessions and rare counted in messages.
//
// Paying for coverage on the forgiving reading pays a session for the words a
// question happens to be phrased with. "Can you suggest a hotel for my trip" is
// four such words and one that matters.
func TestCoverageIsPaidOnTheWordsThatIdentify(t *testing.T) {
	// Session 1 covers three ordinary words and nothing else; session 2 carries
	// the one word that identifies.
	all := map[uint32]int{1: 3, 2: 1}
	identifying := map[uint32]int{2: 1}

	got := coverageCounts(all, identifying, 1)
	if got[1] != 0 {
		t.Errorf("a session covering only ordinary words was still paid for "+
			"covering %d of them", got[1])
	}
	if got[2] != 1 {
		t.Errorf("the session carrying the identifying word lost its coverage: %d", got[2])
	}
}

// A question made entirely of ordinary words has nothing to count strictly, and
// counting them is all there is: dropping to zero coverage everywhere would
// leave the ranking with one less signal exactly where it is thinnest.
func TestCoverageKeepsTheGenerousCountWhenNothingIdentifies(t *testing.T) {
	all := map[uint32]int{1: 3, 2: 2}
	got := coverageCounts(all, map[uint32]int{}, 0)
	if got[1] != 3 || got[2] != 2 {
		t.Errorf("the generous count was dropped on a query with no identifying "+
			"word: %v", got)
	}
}
