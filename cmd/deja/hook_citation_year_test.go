package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The citation line is pre-written for the agent to say aloud, and it dropped
// the year: a decision from July 2025 was narrated as "Jul 3", which reads as
// five weeks ago. Age is the whole question on an old recall — the user has to
// hear that the memory is a year old to judge whether it still holds (#R13).
func TestCitationLineKeepsTheYearOfAnOldRecall(t *testing.T) {
	old := model.Session{Harness: "claude", Updated: time.Now().AddDate(-1, -1, 0),
		Messages: []model.Message{{Role: "user", Text: "why does the reconciler double count refunds"}}}
	line := citationLine(old, nil)
	year := old.Updated.Local().Format("2006")
	if !strings.Contains(line, year) {
		t.Errorf("a %s recall is narrated without its year: %q", year, line)
	}

	// This year's sessions keep the short form: the year adds nothing there
	// and the line is read aloud.
	recent := old
	recent.Updated = time.Now().AddDate(0, 0, -3)
	line = citationLine(recent, nil)
	if strings.Contains(line, recent.Updated.Local().Format("2006")) {
		t.Errorf("this year's recall carries a redundant year: %q", line)
	}
}
