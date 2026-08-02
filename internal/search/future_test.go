package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A transcript stamped ahead of the clock is newer than everything, so one bad
// timestamp labelled every real session in the project as an earlier attempt —
// and the digest called it "today" while search printed its real date (#880).
func TestASessionDatedAheadIsNotTodayAndSupersedesNothing(t *testing.T) {
	now := time.Now()
	future := stalenessSession("fut1", "api", "connection pool exhausted under load", now.AddDate(1, 0, 0))
	// now itself, not "a couple of hours ago": at 00:30 UTC that is yesterday,
	// and this assertion is about the calendar day, not the offset.
	today := stalenessSession("today1", "api", "connection pool exhausted under load", now)
	old := stalenessSession("old1", "api", "connection pool exhausted under load", now.AddDate(0, 0, -30))

	hits, err := Run([]model.Session{future, today, old}, Options{Query: "connection pool exhausted", All: true})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Hit{}
	for _, h := range hits {
		byID[h.Session.ID] = h
	}
	if got := byID["today1"].Superseded; got != "" {
		t.Errorf("today's work was superseded by a session dated ahead: %q", got)
	}
	// A real one still supersedes: the guard is about the future, not about
	// the label.
	if byID["old1"].Superseded == "" {
		t.Error("a month-old session stopped being marked")
	}

	if got := relativeDay(future.Updated, now); !strings.HasPrefix(got, "dated ") {
		t.Errorf("a session a year ahead reads as %q", got)
	}
	if got := relativeDay(today.Updated, now); got != "today" {
		t.Errorf("today reads as %q", got)
	}
	// A day ahead is clock skew, and reading it as today is the kind answer —
	// the behaviour TestAutoRecallProvenanceDates has always asserted.
	if got := relativeDay(now.AddDate(0, 0, 1), now); got != "today" {
		t.Errorf("a day ahead reads as %q, want today", got)
	}
}

// NoteBucketDay decides which lines get the bucket's own date, so its refusals
// matter as much as its hits: a transcript, a short id, a shape that only looks
// like a date (#883).
func TestNoteBucketDayRefusals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		harness string
		id      string
		want    string
	}{
		{"note", notesHarness, "deja-2026-07-16-edge", "2026-07-16"},
		{"transcript with a bucket-shaped id", "claude", "deja-2026-07-16-edge", ""},
		{"note with a short id", notesHarness, "deja-2026", ""},
		{"note with an unparsable day", notesHarness, "deja-2026-13-99-edge", ""},
		{"note with another prefix", notesHarness, "note-2026-07-16-edge", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := NoteBucketDay(model.Session{Harness: tc.harness, ID: tc.id})
			if tc.want == "" {
				if ok {
					t.Fatalf("accepted %q as a bucket day: %q", tc.id, got)
				}
				return
			}
			if !ok || got != tc.want {
				t.Fatalf("day = %q, %v; want %q", got, ok, tc.want)
			}
		})
	}
	// A day that cannot be parsed anchors nowhere rather than at the epoch's
	// midday.
	if got := noteBucketNoon("not-a-day"); !got.IsZero() {
		t.Errorf("unparsable day anchored at %v", got)
	}
	if got := noteBucketNoon("2026-07-16"); got.Hour() != 12 {
		t.Errorf("bucket anchored at %v, want midday", got)
	}
}
