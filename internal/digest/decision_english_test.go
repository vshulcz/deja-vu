package digest

import "testing"

// The English half of the marker list was missing the shapes English uses to
// close something, and the store the list was measured on is Russian-dominant,
// so the gap read as rare rather than as a hole (#2340).
func TestEnglishDecisionShapesAreRead(t *testing.T) {
	for _, line := range []string{
		"we settled on ninety seconds for the skew window",
		"in the end we went with the vpn resolver",
		"we ended up reading the cycle count from ioreg",
		"opted for noto as the fallback font",
		"Decision: the ledger rejects a batch over ninety seconds",
	} {
		if !CarriesDecision(line) {
			t.Errorf("not read as a decision: %q", line)
		}
	}
	// And a line that only plans one is still not a decision.
	for _, line := range []string{
		"i will look at the skew window later",
		"what should we do about the resolver",
	} {
		if CarriesDecision(line) {
			t.Errorf("read as a decision: %q", line)
		}
	}
}
