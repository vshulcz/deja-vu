package main

import "testing"

// A match resting on one rare word gets half the room a two-word match gets:
// it is the weaker claim and the one that fires on unrelated prompts.
func TestWeakMatchGetsHalfTheBlock(t *testing.T) {
	full := digestBudget(true)
	weak := digestBudget(false)
	if full != promptHookBudget-recallFrameOverhead {
		t.Errorf("a confident match must get the whole budget, got %d", full)
	}
	if weak >= full {
		t.Errorf("a weak match got %d of %d — no room was saved", weak, full)
	}
	if weak*2 != full {
		t.Errorf("a weak match got %d, which is not half of %d", weak, full)
	}
	// Still enough to quote something: a block that cannot hold one line is a
	// pointer, and that is a different decision made elsewhere.
	if weak < 300 {
		t.Errorf("a weak match got %d, too little to quote anything", weak)
	}
}
