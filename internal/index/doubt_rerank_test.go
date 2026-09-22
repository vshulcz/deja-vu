package index

import "testing"

// The per-message reading is priced for a big store and skipped on a small one,
// where the session-level overlap usually ranks well enough — usually, and the
// exception is what this gate is for. A crowded top is the case where it does
// not: on LoCoMo's relevance tier a relative distance under 0.05 between the
// first two scores covers 165 questions of which 27% were answered right,
// against 94% where the distance is 0.60 or more.
//
// So the two halves both have to hold: a crowded top is doubtful, a clear one is
// not. A gate that says yes to everything is the "fuse everywhere" setting,
// which the sweep beside doubtRerankGap measures as worse on both benchmarks.
func TestACrowdedTopIsDoubtfulAndAClearOneIsNot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scores []float64
		want   bool
	}{
		{"the second is on the first's heels", []float64{4.0, 3.9}, true},
		{"the first is clear of the field", []float64{4.0, 0.9}, false},
		{"exactly at the gap is not under it", []float64{1.0, 1 - doubtRerankGap}, false},
		{"one candidate has no second to crowd it", []float64{4.0}, false},
		{"a scoreless ranking is not a doubtful one", nil, false},
		// A top score of zero divides to NaN, which compares false on its own,
		// so only a negative one shows what the guard is holding: without it
		// the distance comes out negative and every such ranking reads as
		// crowded.
		{"a zero top score says nothing either way", []float64{0, 0}, false},
		{"a negative top score is not a crowded one", []float64{-1, -2}, false},
	} {
		if got := rankingIsDoubtful(relevanceRanking{scores: tc.scores}); got != tc.want {
			t.Errorf("%s: doubtful=%v, want %v", tc.name, got, tc.want)
		}
	}
}
