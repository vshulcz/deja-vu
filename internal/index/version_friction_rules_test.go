package index

import "testing"

// Four changes in a row widened what counts as a wall — the quote rule, the
// generic prefix, nine phrases, the line cap — and each declined a version
// bump. The signatures live in the manifest, and nothing re-derives them: a
// store built under the old rules keeps answering by them, so `hook-tool-after`
// stays silent and the environment block stays empty on the machine that has
// been running deja longest (#2444).
func TestTheVersionMovesWhenTheRulesDo(t *testing.T) {
	// 30 was the last bump (masking digit runs, #2369). Everything since has
	// changed which lines become signatures at all.
	if version <= 30 {
		t.Errorf("version is %d: the friction rules moved and the version did not, "+
			"so every index built before them answers by the old rules forever", version)
	}
}
