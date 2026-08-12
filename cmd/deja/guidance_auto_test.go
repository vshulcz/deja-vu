package main

import (
	"strings"
	"testing"
)

// An -auto target is the same harness with hooks on top, so it must land on the
// same guidance file. Three targets used to be listed by hand and the other nine
// silently wrote no guidance at all, including through `deja install --auto`
// (#1199). Walking every target means the next harness cannot be forgotten.
func TestAutoTargetsShareTheirHarnessGuidance(t *testing.T) {
	hermeticEnv(t)
	checked := 0
	for _, target := range installTargetNames() {
		if !strings.HasSuffix(target, "-auto") {
			continue
		}
		base := strings.TrimSuffix(target, "-auto")
		if base == "claude" {
			base = "claude-code"
		}
		want := guidancePath(base)
		if want == "" {
			continue
		}
		checked++
		if got := guidancePath(guidanceHarness(target)); got != want {
			t.Errorf("%s guidance = %q, base %q writes %q", target, got, base, want)
		}
	}
	if checked < 5 {
		t.Fatalf("only %d -auto targets have a guidance file — the walk is not covering them", checked)
	}
}
