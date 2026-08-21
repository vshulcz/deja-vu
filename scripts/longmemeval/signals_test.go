package main

import "testing"

// The gate reads one number per candidate, not per ranking: the best any single
// session managed. Summing or averaging across candidates would let a pile of
// weak sessions look like one strong one.
func TestBestSignalsTakesTheStrongestCandidate(t *testing.T) {
	matched := []int{1, 3, 2}
	strong := []int{0, 1, 0}
	best, bestStrong := bestSignals(matched, strong)
	if best != 3 {
		t.Errorf("best matched is 3, got %d", best)
	}
	if bestStrong != 1 {
		t.Errorf("best strong is 1, got %d", bestStrong)
	}
	if best, bestStrong = bestSignals(nil, nil); best != 0 || bestStrong != 0 {
		t.Errorf("an empty ranking has no signal, got %d/%d", best, bestStrong)
	}
	// A ranking whose strong slice is shorter must not read past it.
	if _, bestStrong = bestSignals([]int{2, 2}, []int{1}); bestStrong != 1 {
		t.Errorf("short strong slice mishandled, got %d", bestStrong)
	}
}
