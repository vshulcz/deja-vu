package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// fitBriefWhen cuts the project out of a line and pastes the result back
// between byte offsets taken from the original string. Since #1825 the cut
// returns composed text, so the middle of that splice is not the bytes it
// replaced — the offsets belong to the original, which is what keeps it safe,
// and this says so (#1842).
func TestTheBriefSpliceSurvivesADecomposedProjectName(t *testing.T) {
	const acute, diaeresis = "́", "̈"
	for _, project := range []string{
		"u" + diaeresis + "ber-server-project-with-a-long-name",
		"über-server-project-with-a-long-name",
		"cafe" + acute + "-deploy-pipeline-project-name-here",
	} {
		line := "worked in " + project + " · last worked 3 days ago"
		// Below 55 the guard keeps the line whole (there would be nothing
		// readable left of the name); 55 and up are the widths that actually
		// splice, which is what this test is about.
		for _, room := range []int{30, 55, 60, 65, 70, 75, 80} {
			got := fitBriefWhen(line, room)
			if !utf8.ValidString(got) {
				t.Errorf("the splice produced invalid UTF-8 at room=%d: %q", room, got)
			}
			// The facts after the name are what the line exists for, so the
			// splice must hand them back whole however the middle was cut.
			if !strings.HasSuffix(got, " · last worked 3 days ago") {
				t.Errorf("room=%d: the splice damaged the tail of the line: %q", room, got)
			}
			if !strings.HasPrefix(got, "worked in ") {
				t.Errorf("room=%d: the splice damaged the head of the line: %q", room, got)
			}
			if strings.Contains(got, "�") {
				t.Errorf("the splice produced a replacement character at room=%d: %q", room, got)
			}
			// Where a cut happened, the name comes back composed — that is
			// what the measurement and the cut now agree on. Where none did,
			// the line is the original and keeps whatever spelling it had:
			// this function does not normalise what it passes through.
			if strings.Contains(got, "…") &&
				(strings.Contains(got, "u\u0308") || strings.Contains(got, "e\u0301")) {
				t.Errorf("room=%d: a cut name kept its decomposed spelling: %q", room, got)
			}
		}
	}
}
