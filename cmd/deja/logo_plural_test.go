package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/stats"
)

// A first run on a fresh machine has one session and one message, and the
// greeting counted them in plural: "1 messages across 1 agents", three words
// from a brief that said "1 session across 1 agent" (#1598).
func TestGreetingCountsInSingularAtOne(t *testing.T) {
	one := index.BuildSummary{
		Messages:   1,
		Sessions:   1,
		Harnesses:  1,
		PerHarness: []index.HarnessCount{{Name: "claude", Messages: 1, Sessions: 1}},
	}
	for _, line := range firstIndexInfo(one, `try: deja "something you fixed weeks ago"`) {
		text := visibleText(line)
		for _, wrong := range []string{"1 messages", "1 agents", "1 sessions"} {
			if strings.Contains(text, wrong) {
				t.Errorf("greeting says %q: %q", wrong, text)
			}
		}
	}

	many := index.BuildSummary{
		Messages:   4,
		Sessions:   2,
		Harnesses:  3,
		PerHarness: []index.HarnessCount{{Name: "claude", Messages: 4, Sessions: 2}},
	}
	joined := strings.Join(firstIndexInfo(many, ""), "\n")
	for _, want := range []string{"4 messages", "3 agents", "2 sessions"} {
		if !strings.Contains(visibleText(joined), want) {
			t.Errorf("greeting lost its plural at more than one: %q missing from\n%s", want, joined)
		}
	}
}

// The whole stats screen has to agree with itself: review found "1 sessions
// 1 messages" under By harness, eight lines above a Highlights block that had
// just been fixed to say "1 message" (#1598).
func TestStatsScreenCountsInSingularAtOne(t *testing.T) {
	var buf bytes.Buffer
	printStats(&buf, stats.Report{
		TotalSessions: 1,
		TotalMessages: 1,
		Harnesses:     []stats.HarnessStats{{Harness: "claude", Sessions: 1, Messages: 1}},
		Longest:       stats.SessionStat{Messages: 1, Harness: "claude", Title: "why the migration lock times out"},
		BusiestDay:    stats.DayStat{Date: "2026-08-23", Messages: 1},
		WeekRecalls:   1,
		WeekInjected:  1,
	})

	for _, line := range strings.Split(buf.String(), "\n") {
		text := visibleText(line)
		for _, wrong := range []string{"1 sessions", "1 messages", "1 recalls", "1 auto-injections", "1 prompts"} {
			if strings.Contains(text, wrong) {
				t.Errorf("stats says %q: %q", wrong, text)
			}
		}
	}
}
