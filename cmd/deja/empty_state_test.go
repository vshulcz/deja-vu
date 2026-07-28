package main

import (
	"bytes"
	"github.com/vshulcz/deja-vu/internal/stats"
	"strings"
	"testing"
)

// A fresh install has nothing indexed, and the two commands people try first
// answered with silence and with section headings over nothing. Both read as
// broken rather than empty.
func TestStatsSaysWhatIsMissingWhenNothingIsIndexed(t *testing.T) {
	var out bytes.Buffer
	printStats(&out, stats.Report{})
	got := out.String()
	if !strings.Contains(got, "nothing indexed yet") {
		t.Fatalf("no explanation on an empty index:\n%s", got)
	}
	// The headings are the part that made it look like a broken report.
	for _, heading := range []string{"By harness", "Top projects", "Last 12 months"} {
		if strings.Contains(got, heading) {
			t.Fatalf("empty section %q printed over an empty index:\n%s", heading, got)
		}
	}
	if !strings.Contains(got, "deja index") {
		t.Fatalf("no next step offered:\n%s", got)
	}
}

// A report with data must still print everything, or this guard has cost the
// feature rather than fixed it.
func TestStatsStillPrintsSectionsWithData(t *testing.T) {
	var out bytes.Buffer
	printStats(&out, stats.Report{TotalSessions: 3, TotalMessages: 40})
	got := out.String()
	for _, heading := range []string{"By harness", "Top projects"} {
		if !strings.Contains(got, heading) {
			t.Fatalf("section %q went missing with data present:\n%s", heading, got)
		}
	}
}

func TestEmptyIndexHintNamesTheNextCommand(t *testing.T) {
	got := emptyIndexHint("no sessions indexed yet")
	for _, want := range []string{"no sessions indexed yet", "deja index", "deja doctor"} {
		if !strings.Contains(got, want) {
			t.Fatalf("hint missing %q: %q", want, got)
		}
	}
}
