package main

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/stats"
)

// The repeat count comes from the corpus, so a fresh install has it; the
// week's recalls need the usage sidecar. The card leads with the figure it
// can always show, and the week moves to the supporting row.
func TestCardLeadsWithRepeatQuestions(t *testing.T) {
	value, caption := heroStat(stats.Report{RepeatQuestions: 26, WeekRecalls: 428})
	if value != "26" || !strings.Contains(caption, "more than once") {
		t.Fatalf("hero = %q %q, want the repeat count", value, caption)
	}
	// The caption sits left of the agents column at 15 px; past about forty
	// characters it runs into "WHERE IT CAME FROM".
	for _, r := range []stats.Report{{RepeatQuestions: 26}, {WeekRecalls: 428}, {TotalSessions: 12}, {PolicyWithheld: 3}} {
		if _, c := heroStat(r); len(c) > 44 {
			t.Fatalf("caption %q is %d characters, past the agents column", c, len(c))
		}
	}
	svg := renderStatsCard(stats.Report{RepeatQuestions: 26, WeekRecalls: 428, TotalSessions: 10})
	if !strings.Contains(svg, "428 recalls handed to your agents this week") {
		t.Fatalf("the week's recalls left the card:\n%s", svg)
	}
	if !strings.Contains(svg, "github.com/vshulcz/deja-vu") {
		t.Fatalf("the footer no longer points at the repository:\n%s", svg)
	}
}

// Two rows of text at the same y read as one smeared row (#3060): every
// text element in the bottom band has to sit on its own line, or on the
// footer's line only if it is the footer.
func TestCardRowsNeverShareTheFooterLine(t *testing.T) {
	r := stats.Report{RepeatQuestions: 26, WeekRecalls: 428, TotalSessions: 10}
	r.Longest.Messages = 45051
	svg := renderStatsCard(r)
	re := regexp.MustCompile(`<text[^>]*\by="(\d+)"[^>]*>([^<]*)`)
	byY := map[int][]string{}
	for _, m := range re.FindAllStringSubmatch(svg, -1) {
		y, _ := strconv.Atoi(m[1])
		byY[y] = append(byY[y], m[2])
	}
	// The footer's line carries the footer and nothing else.
	for y, texts := range byY {
		foot, other := 0, 0
		for _, tx := range texts {
			if strings.HasPrefix(tx, "$ deja") || strings.HasPrefix(tx, "github.com") {
				foot++
			} else {
				other++
			}
		}
		if foot > 0 && other > 0 {
			t.Fatalf("y=%d carries the footer and %v", y, texts)
		}
	}
	// And the supporting row is not on the footer's line.
	foot := regexp.MustCompile(`y="(\d+)"[^>]*>\$ deja stats`).FindStringSubmatch(svg)
	row := regexp.MustCompile(`y="(\d+)"[^>]*>THE LONGEST SESSION`).FindStringSubmatch(svg)
	if foot == nil || row == nil {
		t.Fatalf("footer or row missing:\n%s", svg)
	}
	fy, _ := strconv.Atoi(foot[1])
	ry, _ := strconv.Atoi(row[1])
	if fy-ry < 12 {
		t.Fatalf("row at y=%d sits on the footer at y=%d", ry, fy)
	}
}
