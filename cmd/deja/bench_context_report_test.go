package main

import (
	"bytes"
	"strings"
	"testing"
)

// The union arm cannot fall until both surfaces under it do — each carries the
// chain's facts alone, which is how four PR bodies came to cite "coverage 1.00
// unchanged" as evidence about a digest that was emitting nothing (#2931). The
// report says which rows to read instead.
func TestTheContextReportSaysTheUnionRowIsAFloor(t *testing.T) {
	report := contextReport{
		Chains: 3, Negatives: 1,
		Arms: map[string]contextArmReport{
			"deja-recall": {MedianTokens: 563, MedianCoverage: 1},
			"deja-digest": {MedianTokens: 294, MedianCoverage: 1},
			"deja-block":  {MedianTokens: 269, MedianCoverage: 0.67},
		},
	}
	var out bytes.Buffer
	printContextReport(&out, report)
	got := out.String()
	for _, arm := range []string{"deja-recall", "deja-digest", "deja-block"} {
		if !strings.Contains(got, arm) {
			t.Fatalf("the %s row is missing:\n%s", arm, got)
		}
	}
	if !strings.Contains(got, "union of the two rows under it") {
		t.Fatalf("the report does not say the union row is a floor:\n%s", got)
	}
}
