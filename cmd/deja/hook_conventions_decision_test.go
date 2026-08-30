package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The block tells an agent to follow these lines. A promoted note's title is
// the session's own first line — usually the problem — while what was decided
// is the note the person wrote with `--note`. Printing the title handed the
// agent the question as the answer: "follow: the migration keeps failing on
// retry" (#2456).
func TestAStandingDecisionIsTheDecisionNotTheProblem(t *testing.T) {
	note := sources.PromotedNote{
		Project: "work/app",
		State:   "accepted",
		Title:   "the migration keeps failing on retry",
		Text:    "retry budget stays at 5 after the pool change",
		At:      time.Now(),
	}
	line := conventionLine(note)
	if !strings.Contains(line, "retry budget stays at 5") {
		t.Errorf("the line an agent is told to follow does not carry the decision: %q", line)
	}
	if strings.HasPrefix(line, "the migration keeps failing") {
		t.Errorf("the line leads with the problem: %q", line)
	}

	// A note with nothing but a title still says what it has.
	titleOnly := sources.PromotedNote{Project: "work/app", State: "accepted",
		Title: "keep the pool at 8", At: time.Now()}
	if got := conventionLine(titleOnly); !strings.Contains(got, "keep the pool at 8") {
		t.Errorf("a note that is only a title said %q", got)
	}

	// And one with neither says nothing rather than an empty bullet.
	if got := conventionLine(sources.PromotedNote{Project: "work/app", State: "accepted"}); got != "" {
		t.Errorf("an empty note produced %q", got)
	}
}
