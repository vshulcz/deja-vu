package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/usage"
)

// `deja stats --json` reports four week figures and the document never said
// what a week is; the rules lived only in Go, and until #1921 deja itself did
// not agree with them (#1939). This holds the sentence against the code it
// describes rather than against itself.
func TestTheStatsSectionSaysWhatAWeekIs(t *testing.T) {
	doc, err := os.ReadFile("../../docs/json-output.md")
	if err != nil {
		t.Fatal(err)
	}
	section := docSection(t, string(doc), "## `deja stats --json`")
	prose := strings.Join(strings.Fields(section), " ")

	for _, want := range []struct{ what, phrase string }{
		{"a week is seven calendar days", "seven calendar days"},
		{"a day starts at local midnight", "local midnight"},
		{"the wall clock is what is kept across a change", "wall clock"},
	} {
		if !strings.Contains(prose, want.phrase) {
			t.Errorf("the stats section does not say %s (looked for %q)", want.what, want.phrase)
		}
	}

	// And the sentence has to keep describing what WeekCut does: seven calendar
	// days at the same wall time, not a fixed number of hours.
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata: ", err)
	}
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, ny)
	if cut := usage.WeekCut(now); cut.Format("15:04:05") != now.Format("15:04:05") {
		t.Errorf("WeekCut no longer keeps the wall clock (%s from %s), so the documented sentence is wrong",
			cut.Format("15:04:05 MST"), now.Format("15:04:05 MST"))
	}
	if cut, fixed := usage.WeekCut(now), now.Add(-7*24*time.Hour); cut.Equal(fixed) {
		t.Error("WeekCut and a fixed 168 hours agree here, so this test proves nothing about the rule")
	}
}
