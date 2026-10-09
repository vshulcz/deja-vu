package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/stats"
)

// The page and the terminal made different claims from one report: "You asked
// the same thing 4 times" where the count is four questions asked more than
// once, and the busiest month where the terminal names the busiest day.
func TestStatsPageMakesTheTerminalsClaims(t *testing.T) {
	r := stats.Report{
		TotalSessions:   3,
		TotalMessages:   200,
		RepeatQuestions: 4,
		BusiestDay:      stats.DayStat{Date: "2025-12-10", Messages: 38},
		Monthly:         []stats.MonthStats{{Month: "2026-06", Messages: 152}},
	}
	path := filepath.Join(t.TempDir(), "stats.html")
	if _, err := writeStatsHTML(path, r, nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	for _, want := range []string{"4 questions more than once", "BUSIEST DAY 2025-12-10, 38 MESSAGES"} {
		if !strings.Contains(page, want) {
			t.Errorf("page is missing %q", want)
		}
	}
	for _, not := range []string{"the same thing 4 times", "BUSIEST 2026-06"} {
		if strings.Contains(page, not) {
			t.Errorf("page still says %q", not)
		}
	}
}
