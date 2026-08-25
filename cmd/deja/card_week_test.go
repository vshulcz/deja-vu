package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/stats"
)

// Four surfaces print this week's recalls — `deja stats`, `--json`, the card and
// the page — and deja had two rules for what a week is until #1921. They agree
// now because each renders the field the report carries rather than asking the
// usage log again. This holds that: hand the renderers a figure and it is the
// figure they show.
func TestTheCardAndThePagePrintTheWeekTheyWereHanded(t *testing.T) {
	r := stats.Report{TotalSessions: 40, TotalMessages: 400, WeekRecalls: 37}

	card := renderStatsCard(r)
	if !strings.Contains(card, ">37</text>") || !strings.Contains(card, "this week") {
		t.Errorf("the card does not show the week it was given")
	}

	page := renderPage(t, r)
	if !strings.Contains(page, "37 times this week") {
		t.Errorf("the page does not show the week it was given")
	}

	// A four-figure week, since formatStatNumber groups thousands: the
	// renderers must still be showing what they were handed, and a test that
	// only ever passes two-digit numbers would not notice a renderer that
	// formatted its own.
	r.WeekRecalls = 1234
	if card := renderStatsCard(r); !strings.Contains(card, ">1,234</text>") {
		t.Errorf("the card does not group thousands the way the report's other figures are grouped")
	}
	if page := renderPage(t, r); !strings.Contains(page, "1,234 times this week") {
		t.Errorf("the page does not group thousands")
	}

	// A different figure in, a different figure out — so neither row above can
	// be a constant that happens to match.
	r.WeekRecalls = 2
	card = renderStatsCard(r)
	if !strings.Contains(card, ">2</text>") {
		t.Errorf("the card ignored the second figure")
	}
	if strings.Contains(card, ">37</text>") {
		t.Errorf("the card still shows the first figure")
	}
	page = renderPage(t, r)
	if !strings.Contains(page, "2 times this week") || strings.Contains(page, "37 times this week") {
		t.Errorf("the page ignored the second figure")
	}
}

func renderPage(t *testing.T, r stats.Report) string {
	t.Helper()
	path, err := writeStatsHTML(filepath.Join(t.TempDir(), "stats.html"), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
