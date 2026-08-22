package index

import (
	"math"
	"testing"
)

// Rarity answers two questions and one number cannot serve both.
//
// "Is this term worth speaking up about" takes the rarer of the two verdicts —
// counted in sessions and counted in messages with each session capped — so a
// subject word does not read as filler merely because the marathons that hold
// most of a store all mention it.
//
// "What is this match worth" is the ranking weight, and there the capped-message
// view is wrong: it lifts every term a few long sessions happen to repeat and
// reorders the top of the result. Measured on LongMemEval-S, using the gate's
// number to weight the score cost 1.7 points of hit@1 (84.9 -> 83.2), with
// preference questions falling from 36.7% to 26.7%. Splitting them scores 85.3%
// while the gate keeps the recall it was introduced for.
func TestTheTwoRarityRolesStaySeparate(t *testing.T) {
	// A store of 1500 sessions; the term lives in 16 of them, and its messages
	// are concentrated enough that the capped-message view calls it much rarer.
	const sessions, minSess = 1500, 16
	totalDocs, minDF := 20000.0, 48.0

	rank := rankIDF(sessions, minSess)
	gate := gateIDF(totalDocs, int(minDF), rank)

	wantRank := math.Log(float64(sessions+1) / float64(minSess+1))
	if math.Abs(rank-wantRank) > 1e-9 {
		t.Errorf("rank weight = %.4f, want documents counted in sessions (%.4f)", rank, wantRank)
	}
	if gate <= rank {
		t.Errorf("the gate scores %.4f against the ranking's %.4f — it is not taking the rarer verdict", gate, rank)
	}

	// The other direction: a word that saturates one long session. Counted in
	// capped messages it looks common; the session count is what keeps it rare,
	// and the gate has to follow whichever says rare.
	saturating := gateIDF(400, 25, rankIDF(120, 1))
	if saturating < rankIDF(120, 1)-1e-9 {
		t.Errorf("a word filling one session scores %.4f, below its session verdict %.4f", saturating, rankIDF(120, 1))
	}

	// And the weight never inherits that: whatever the gate decides, the score
	// is the session view.
	if w := rankIDF(120, 1); math.Abs(w-math.Log(121.0/2.0)) > 1e-9 {
		t.Errorf("rank weight drifted: %.4f", w)
	}
}
