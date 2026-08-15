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
		"agents", "TOP AGENTS", "opencode", "1013"} {
		if !strings.Contains(out, want) {
			t.Errorf("the card never shows %q", want)
		}
	}
}

// The agents are ranked, not printed in whatever order the report held them:
// a card that lists the smallest first says the wrong thing about the history.
func TestCardRanksAgentsBySessions(t *testing.T) {
	out := statsCardLines(sampleReport())
	var order []string
	for _, line := range out {
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

// The heatmap starts where the history does. Drawing the empty half of a year
// spends most of the card on a grey rectangle standing for nothing.
func TestCardHeatmapSkipsTheEmptyRunUpFront(t *testing.T) {
	lines := heatLines(sampleReport().Heatmap)
	if len(lines) == 0 {
		t.Fatal("no heatmap drawn")
	}
	// four rows of half blocks for seven days, plus the month labels
	if len(lines) != 5 {
		t.Errorf("heatmap is %d lines, want 5", len(lines))
	}
	for _, line := range lines[1:] {
		if n := strings.Count(line, "▀"); n > 20 {
			t.Errorf("heatmap draws %d weeks; the report has 13 with anything in them", n)
		}
	}
}

// An empty report must still produce a card rather than a panic: a first run
// with nothing indexed is exactly when someone tries this.
func TestCardSurvivesAnEmptyReport(t *testing.T) {
	out := strings.Join(statsCardLines(stats.Report{}), "\n")
	if out == "" {
		t.Fatal("empty report produced no card at all")
	}
	if !strings.Contains(out, "deja-vu") {
		t.Error("even an empty card should carry the mark")
	}
}
