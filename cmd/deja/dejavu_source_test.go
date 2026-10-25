package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The line never said which agent the memory came from, which is the one
// thing a single agent's own memory cannot say.
func TestDejaVuLineNamesTheAgentAndTheAge(t *testing.T) {
	ago := func(days int) time.Time { return time.Now().Add(-time.Duration(days)*24*time.Hour - time.Hour) }
	s := model.Session{ID: "c", Harness: "codex", Project: "app", Updated: ago(125),
		Messages: []model.Message{{Role: "user", Text: "the stampede fix: jittered ttl on the jwks cache"}}}
	if got, want := dejaVuLine(s), `deja-vu — from Codex CLI, 4 months ago: "the stampede fix: jittered ttl on the jwks cache"`; got != want {
		t.Fatalf("line = %q\nwant  %q", got, want)
	}
	for _, c := range []struct {
		days int
		want string
	}{
		{9, "1 week ago"},
		{20, "2 weeks ago"},
		{70, "2 months ago"},
		{400, "1 year ago"},
		{800, "2 years ago"},
	} {
		if got := dejaVuAge(ago(c.days)); got != c.want {
			t.Errorf("%d days: %q, want %q", c.days, got, c.want)
		}
	}
	// Under a week is counted in calendar days, which the hour of the run moves.
	if got := dejaVuAge(ago(3)); !strings.HasSuffix(got, "d ago") {
		t.Errorf("three days reads as %q", got)
	}
	if got := dejaVuAge(time.Now()); got != "today" {
		t.Errorf("now reads as %q", got)
	}
	if got := dejaVuAge(time.Time{}); got != "" {
		t.Errorf("no date reads as %q", got)
	}
	notes := s
	notes.Harness = "deja"
	if got := dejaVuLine(notes); !strings.HasPrefix(got, "deja-vu — from your notes, 4 months ago: ") {
		t.Errorf("a promoted note is credited to an agent called deja: %q", got)
	}
	bare := model.Session{ID: "b", Messages: []model.Message{{Role: "user", Text: "drain the queue first"}}}
	if got := dejaVuLine(bare); got != `deja-vu — you have been here: "drain the queue first"` {
		t.Errorf("with neither agent nor date: %q", got)
	}
}
