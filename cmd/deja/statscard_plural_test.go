package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/stats"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// The card is meant to be pasted into a README, so "1 sessions" is read by
// everyone who visits the repo, not just its owner.
func TestCardPunchlineCountsOne(t *testing.T) {
	cases := []struct {
		name string
		r    stats.Report
		want string
	}{
		{"sessions", stats.Report{TotalSessions: 1}, "1 session of agent history, all searchable."},
		{"week recalls", stats.Report{WeekRecalls: 1}, "deja handed your agents memory 1 time this week."},
		{"repeat questions", stats.Report{RepeatQuestions: 1}, "You asked the same thing 1 time — deja remembered."},
		{"recalls", stats.Report{Recall: usage.Summary{Recalls: 1}}, "deja handed your agents memory 1 time."},
	}
	for _, c := range cases {
		if got := cardPunchline(c.r); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCardPunchlineKeepsPluralAboveOne(t *testing.T) {
	if got := cardPunchline(stats.Report{TotalSessions: 4}); !strings.Contains(got, "4 sessions") {
		t.Fatalf("got %q", got)
	}
	if got := cardPunchline(stats.Report{WeekRecalls: 9}); !strings.Contains(got, "9 times") {
		t.Fatalf("got %q", got)
	}
}
