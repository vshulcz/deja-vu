package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/stats"
)

func TestMonthlyTotal(t *testing.T) {
	if got := monthlyTotal(nil); got != 0 {
		t.Errorf("nil = %d", got)
	}
	empty := make([]stats.MonthStats, 12)
	if got := monthlyTotal(empty); got != 0 {
		t.Errorf("twelve empty months = %d", got)
	}
	empty[3].Messages = 5
	empty[9].Messages = 2
	if got := monthlyTotal(empty); got != 7 {
		t.Errorf("total = %d, want 7", got)
	}
}

// Twelve empty bars is what a broken index looks like, and a store whose work
// is simply older than a year drew exactly that (#703).
func TestStatsSparklineSaysWhatItCoversWhenTheYearIsEmpty(t *testing.T) {
	var buf bytes.Buffer
	r := stats.Report{
		TotalSessions: 4,
		Monthly:       make([]stats.MonthStats, 12),
		Sparkline:     "▁▁▁▁▁▁▁▁▁▁▁▁",
		DateRange:     stats.DateRangeStats{Start: "2025-06-24", End: "2025-06-27"},
	}
	printStats(&buf, r)
	out := buf.String()
	if !strings.Contains(out, "none — this store covers Jun 24 2025 → Jun 27 2025") {
		t.Errorf("empty year: %q", sparklineSection(out))
	}
	if strings.Contains(out, "▁▁▁▁▁▁▁▁▁▁▁▁") {
		t.Errorf("still drew the empty sparkline: %q", sparklineSection(out))
	}

	// A store with work inside the window keeps its chart.
	buf.Reset()
	r.Monthly[10].Messages = 8
	r.Sparkline = "▁▁▁▁▁▁▁▁▁▁█▁"
	printStats(&buf, r)
	if !strings.Contains(sparklineSection(buf.String()), "###") {
		t.Errorf("chart lost: %q", sparklineSection(buf.String()))
	}
}

func sparklineSection(out string) string {
	i := strings.Index(out, "Last 12 months")
	if i < 0 {
		return out
	}
	rest := out[i:]
	if j := strings.Index(rest, "\n\n"); j > 0 {
		return rest[:j]
	}
	return rest
}

// Each month's name sits under its own bar: the chart used to be twelve
// one-cell bars followed by twelve labels, so no label was under its bar.
func TestMonthChartPutsEachLabelUnderItsBar(t *testing.T) {
	months := make([]stats.MonthStats, 12)
	for i := range months {
		months[i].Month = fmt.Sprintf("2026-%02d", i+1)
	}
	months[2].Messages = 10
	lines := monthChart(months, false)
	bottom, labels := lines[len(lines)-2], lines[len(lines)-1]
	at := strings.Index(bottom, "#")
	if at < 0 || labels[at:at+3] != "Mar" || bottom[at:at+3] != "###" {
		t.Errorf("bar and label disagree:\n%s\n%s", bottom, labels)
	}
}
