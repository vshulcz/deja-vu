package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/usage"
)

// n=1 is this screen's most common state, not its edge case: it is what a new
// user sees the first time an agent recalls anything, and it read "1
// agent-initiated recalls returned matches" (#1652).
func TestImpactScreenReadsAtOne(t *testing.T) {
	var b bytes.Buffer
	r := usage.ImpactReport{Recalls: 1, Injections: 1, DejaVuMoments: 1, ReusedTwice: 1, RawBytes: 400, ServedBytes: 200}
	if err := printImpact(&b, r, 0, false); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, wrong := range []string{
		"1 agent-initiated recalls",
		"1 session starts",
		"1 prompts",
		"1 sessions recalled",
	} {
		if strings.Contains(out, wrong) {
			t.Errorf("reads %q:\n%s", wrong, out)
		}
	}
	for _, want := range []string{
		"1 agent-initiated recall ",
		"1 session start began",
		"1 prompt matched",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	// The control: at two the plural comes back.
	b.Reset()
	r = usage.ImpactReport{Recalls: 2, Injections: 2, DejaVuMoments: 2, ReusedTwice: 2, RawBytes: 400, ServedBytes: 200}
	if err := printImpact(&b, r, 0, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2 agent-initiated recalls", "2 session starts", "2 prompts matched"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("plural lost at two: missing %q:\n%s", want, b.String())
		}
	}
}
