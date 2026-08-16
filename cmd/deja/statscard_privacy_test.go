package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/stats"
)

// The card exists to be published: it prints an embed snippet and invites the
// reader to paste it into a README. What makes that safe is that it carries
// counts and nothing else — no project name, no path, no sentence out of the
// reader's own history.
//
// That property is easy to trade away for a card that feels more personal, and
// it was traded away once: the longest session's title was printed on both
// cards for a day. This is the test that would have caught it.
func TestNeitherCardPrintsContentFromTheHistory(t *testing.T) {
	const (
		title   = "продолжай DDD-рефактор для платёжного шлюза"
		project = "acme-payments-internal"
	)
	r := stats.Report{
		TotalSessions:   1225,
		TotalMessages:   139970,
		RepeatQuestions: 18,
		WeekRecalls:     70,
		Longest: stats.SessionStat{
			Title: title, Project: project, Harness: "claude", Messages: 31868,
		},
		TopProjects: []stats.ProjectStats{{Project: project, Sessions: 400}},
		Harnesses:   []stats.HarnessStats{{Harness: "claude", Sessions: 62}},
	}
	r.DateRange.Start, r.DateRange.End = "2026-04-18", "2026-08-15"
	r.Heatmap.Max = 8
	r.Heatmap.Weeks = make([][7]int, 53)
	for w := 40; w < 53; w++ {
		r.Heatmap.Weeks[w][0] = 3
	}

	for name, out := range map[string]string{
		"the terminal card": strings.Join(statsCardLines(r), "\n"),
		"the SVG card":      renderStatsCard(r),
	} {
		for _, secret := range []string{title, project} {
			if strings.Contains(out, secret) {
				t.Errorf("%s prints %q, which came out of the reader's own history", name, secret)
			}
		}
		// the counts derived from those sessions are fine, and are the point
		if !strings.Contains(out, "31,868") {
			t.Errorf("%s dropped the longest session's size along with its title", name)
		}
		if !strings.Contains(out, "18") {
			t.Errorf("%s does not show how many questions were asked more than once", name)
		}
	}
}
