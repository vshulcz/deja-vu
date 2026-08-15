package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/stats"
)

func sampleReport() stats.Report {
	r := stats.Report{
		TotalSessions: 1225,
		TotalMessages: 139970,
		Harnesses: []stats.HarnessStats{
			{Harness: "claude", Sessions: 62},
			{Harness: "opencode", Sessions: 1013},
			{Harness: "codex", Sessions: 32},
		},
	}
	// A real report carries a date range, and the footer grows by it. Without
	// one here the straight-border test passed while the shipped card pushed
	// its right edge eight columns off the frame.
	r.DateRange.Start = "2026-04-18"
	r.DateRange.End = "2026-08-15"
	r.WeekRecalls = 70
	// A Cyrillic title is the case that broke the width maths: 22 printed
	// columns and 40 bytes. Without one here the border test measured only
	// ASCII and passed while the shipped card came out short.
	r.Longest = stats.SessionStat{Title: "продолжай DDD-рефактор", Messages: 31868}
	r.Heatmap.Max = 8
	r.Heatmap.Weeks = make([][7]int, 53)
	for w := 40; w < 53; w++ {
		for d := 0; d < 7; d++ {
			r.Heatmap.Weeks[w][d] = (w + d) % 9
		}
	}
	r.Heatmap.Months = []stats.HeatMonth{{Col: 40, Label: "Jun"}, {Col: 48, Label: "Aug"}}
	return r
}

func TestCardDrawsTheFiguresAndTheAgents(t *testing.T) {
	out := strings.Join(statsCardLines(sampleReport()), "\n")
	for _, want := range []string{"deja-vu", "1,225", "139,970", "sessions", "messages",
		"agents", "WHERE IT CAME FROM", "opencode", "1,013", "ACTIVITY"} {
		if !strings.Contains(out, want) {
			t.Errorf("the card never shows %q", want)
		}
	}
}

// Every line has to be the same width or the right border walks down the page,
// and the bytes in a coloured string say nothing about how wide it prints.
func TestCardBorderIsStraight(t *testing.T) {
	lines := statsCardLines(sampleReport())
	want := visibleLen(lines[0])
	for i, line := range lines {
		if got := visibleLen(line); got != want {
			t.Errorf("line %d prints %d columns, the top border prints %d:\n%s",
				i, got, want, line)
		}
	}
}

// The agents are ranked, not printed in whatever order the report held them:
// a card that lists the smallest first says the wrong thing about the history.
func TestCardRanksAgentsBySessions(t *testing.T) {
	var order []string
	for _, line := range statsCardLines(sampleReport()) {
		for _, name := range []string{"opencode", "claude", "codex"} {
			if strings.Contains(line, name) {
				order = append(order, name)
			}
		}
	}
	want := []string{"opencode", "claude", "codex"}
	if len(order) != len(want) {
		t.Fatalf("expected each agent once, got %v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("agents are in %v, want %v", order, want)
		}
	}
}

// The grid fills the card at either cell width. Sized to the data alone it took
// a third of the width and read as unfinished; padded to the full year it was
// mostly a grey rectangle standing for history that does not exist.
func TestCardHeatmapFillsTheCard(t *testing.T) {
	for _, tc := range []struct {
		name  string
		weeks int
	}{{"a short history", 13}, {"a full year", 53}} {
		r := stats.Report{}
		r.Heatmap.Max = 8
		r.Heatmap.Weeks = make([][7]int, 53)
		for w := 53 - tc.weeks; w < 53; w++ {
			r.Heatmap.Weeks[w][0] = 3
		}
		lines := heatLines(r.Heatmap)
		if len(lines) != 5 {
			t.Fatalf("%s: heatmap is %d lines, want four rows and the labels", tc.name, len(lines))
		}
		for i, line := range lines[:4] {
			if got := visibleLen(line); got != cardInner {
				t.Errorf("%s: grid row %d prints %d columns, want %d", tc.name, i, got, cardInner)
			}
		}
	}
}

// The punchline is the only sentence on the card. Cutting it at the width
// produced "deja handed your agents", which is not a shorter sentence.
func TestCardPunchlineWrapsRatherThanTruncating(t *testing.T) {
	const line = "deja handed your agents memory 70 times this week."
	got := wrapTo(line, cardInner-26)
	joined := visibleText(got[0]) + " " + visibleText(got[1])
	if !strings.HasPrefix(line, strings.TrimSpace(visibleText(got[0]))) {
		t.Fatalf("first line %q is not the start of the sentence", got[0])
	}
	if !strings.Contains(joined, "this week") {
		t.Errorf("the end of the sentence was dropped: %q", joined)
	}
}

// An empty report must still produce a card rather than a panic: a first run
// with nothing indexed is exactly when someone tries this.
func TestCardSurvivesAnEmptyReport(t *testing.T) {
	lines := statsCardLines(stats.Report{})
	out := strings.Join(lines, "\n")
	if !strings.Contains(out, "deja-vu") {
		t.Error("even an empty card should carry the mark")
	}
	want := visibleLen(lines[0])
	for i, line := range lines {
		if got := visibleLen(line); got != want {
			t.Errorf("empty card line %d prints %d columns, want %d", i, got, want)
		}
	}
}
