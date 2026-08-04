package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A day bucket's date is the day the index was built in; the times inside are
// this machine's. Read in another zone the two disagree, and the screen that
// shows them together said nothing (#1006).
func TestBucketDayIsExplainedOnlyWhenItDiffers(t *testing.T) {
	at := time.Date(2026, 8, 4, 6, 30, 0, 0, time.UTC)
	s := model.Session{Harness: "deja", ID: "deja-2026-08-04-w20", Project: "w20",
		Messages: []model.Message{{Role: "user", Text: "morning: the pool cap is 20", Time: at}}}

	saved := time.Local
	t.Cleanup(func() { time.Local = saved })

	// Read where the record falls on the day the id names: no line.
	time.Local = time.UTC
	if note := bucketDayNote(s); note != "" {
		t.Errorf("a bucket read in its own zone was explained anyway: %q", note)
	}

	// Seven hours west, the same record is the previous day.
	time.Local = time.FixedZone("west", -7*60*60)
	note := bucketDayNote(s)
	if note == "" {
		t.Fatal("nothing explains an id dated a day after the line inside it")
	}
	if !strings.Contains(note, "2026-08-04") || !strings.Contains(note, "day the index was built in") {
		t.Errorf("the note does not say what the id's date is: %q", note)
	}

	// A transcript whose id merely looks like a bucket is not one: only deja's
	// own notes are grouped by day.
	other := s
	other.Harness = "claude"
	if n := bucketDayNote(other); n != "" {
		t.Errorf("a transcript picked up the bucket note: %q", n)
	}
}
