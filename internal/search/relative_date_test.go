package search

import (
	"testing"
	"time"
)

// Work done at 00:30 local time is stored as 21:30 UTC the day before, and
// taking its calendar date in UTC made this morning read as "1d ago" — while
// the counter above it, which compares instants, said "today" (#767).
func TestRelativeDateUsesTheReadersZone(t *testing.T) {
	now := time.Now()
	local := now.Location()

	// This morning, just after local midnight, expressed in UTC.
	morning := time.Date(now.Year(), now.Month(), now.Day(), 0, 30, 0, 0, local)
	if !morning.Before(now) {
		t.Skip("run before 00:30 local time")
	}
	if got := relativeDate(morning.UTC()); got != "today" {
		t.Errorf("this morning in UTC = %q, want \"today\"", got)
	}
	if got := relativeDate(morning); got != "today" {
		t.Errorf("this morning in local time = %q", got)
	}

	// Late yesterday evening is still yesterday however it is stored.
	evening := time.Date(now.Year(), now.Month(), now.Day(), 23, 30, 0, 0, local).AddDate(0, 0, -1)
	for _, ts := range []time.Time{evening, evening.UTC()} {
		if got := relativeDate(ts); got != "1d ago" {
			t.Errorf("yesterday evening (%v) = %q, want \"1d ago\"", ts.Location(), got)
		}
	}

	// A far-off zone must not shift the answer either.
	kiritimati := time.FixedZone("LINT", 14*3600)
	if got := relativeDate(morning.In(kiritimati)); got != "today" {
		t.Errorf("this morning in UTC+14 = %q", got)
	}
	baker := time.FixedZone("AoE", -12*3600)
	if got := relativeDate(morning.In(baker)); got != "today" {
		t.Errorf("this morning in UTC-12 = %q", got)
	}
}
