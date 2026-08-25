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
			// One spelling at every width: a line that fits used to come back
			// as stored while a cut one came back composed, so the same
			// project appeared two ways in one screen (#1844).
			if strings.Contains(got, "u\u0308") || strings.Contains(got, "e\u0301") {
				t.Errorf("room=%d: the decomposed spelling reached the screen: %q", room, got)
			}
		}
	}
}

// The other two brief helpers that cut had the same split: a value that fits
// kept its stored spelling, a cut one came back composed. All three compose
// now, so one screen shows one spelling (#1844).
func TestEveryBriefHelperShowsOneSpelling(t *testing.T) {
	const diaeresis, acute = "̈", "́"
	decomposed := "u" + diaeresis + "ber-server-project"
	long := decomposed + strings.Repeat("-and-more", 8)

	for _, tc := range []struct {
		name string
		got  string
	}{
		{"a title that fits", trimBriefTitleTo("cafe"+acute+" deploy", 60)},
		{"a title that is cut", trimBriefTitleTo("cafe"+acute+" deploy "+strings.Repeat("x", 200), 20)},
		{"a project that fits", fitBriefProject(decomposed, 10)},
		{"a project that is cut", fitBriefProject(long, 40)},
	} {
		if strings.Contains(tc.got, diaeresis) || strings.Contains(tc.got, acute) {
			t.Errorf("%s: the decomposed spelling reached the screen: %q", tc.name, tc.got)
		}
		if tc.got == "" {
			t.Errorf("%s: nothing came back, so this proves nothing", tc.name)
		}
	}
}
