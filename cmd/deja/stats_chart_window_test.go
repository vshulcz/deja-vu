package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/stats"
)

// The chart is a window, and a store that mostly predates it read as if the
// visible bar were the whole shape — while Range, two lines above, named
// months the chart never draws (#854).
func TestStatsSaysHowMuchIsOlderThanTheChart(t *testing.T) {
	months := make([]stats.MonthStats, 12)
	months[0] = stats.MonthStats{Month: "2025-09", Messages: 9}
	r := stats.Report{
		TotalSessions: 40, TotalMessages: 40,
		Monthly:   months,
		Sparkline: "█▁▁▁▁▁▁▁▁▁▁▁",
		DateRange: stats.DateRangeStats{Start: "2025-06-01", End: "2025-09-26"},
	}
	var out strings.Builder
	printStats(&out, r)
	got := out.String()
	if !strings.Contains(got, "31 of 40 messages are older than the chart") {
		t.Errorf("the chart does not say what it leaves out:\n%s", got)
	}

	// A store that fits inside the window says nothing: the chart is the whole
	// shape there, and a line about zero hidden messages is noise.
	inside := r
	inside.TotalMessages = 9
	inside.TotalSessions = 9
	var fits strings.Builder
	printStats(&fits, inside)
	if strings.Contains(fits.String(), "older than the chart") {
		t.Errorf("a store inside the window was told part of it is hidden:\n%s", fits.String())
	}

	// And a store where the window holds most of the work stays quiet too —
	// the line is for the case where the chart misleads, not for arithmetic.
	most := r
	most.TotalMessages = 12
	var mostOut strings.Builder
	printStats(&mostOut, most)
	if strings.Contains(mostOut.String(), "older than the chart") {
		t.Errorf("a mostly-visible store got the warning:\n%s", mostOut.String())
	}
}
