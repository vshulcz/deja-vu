package stats

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A session stamped 22:00 UTC is 01:00 tomorrow for a reader east of UTC. The
// brief has rendered in the reader's zone since #767; stats kept using the
// timestamp's, so two screens of one tool dated the same session a day apart
// (#849).
func TestDateRangeUsesTheReadersZone(t *testing.T) {
	east := time.FixedZone("east", 3*60*60)
	stamp := time.Date(2026, 7, 1, 22, 0, 0, 0, time.UTC)
	ss := []model.Session{{
		ID: "late", Harness: "claude", Project: "p",
		Started: stamp, Updated: stamp,
		Messages: []model.Message{{Role: "user", Text: "late evening session", Time: stamp}},
	}}

	local := stamp.In(east).Format("2006-01-02")
	if local != "2026-07-02" {
		t.Fatalf("fixture zone is wrong: %s", local)
	}

	// Build takes "now", and the zone deja renders in is that instant's.
	got := Build(ss, time.Date(2026, 7, 5, 12, 0, 0, 0, east))
	if got.DateRange.Start != local || got.DateRange.End != local {
		t.Errorf("range = %s → %s, want %s on both ends", got.DateRange.Start, got.DateRange.End, local)
	}
	if got.BusiestDay.Date != local {
		t.Errorf("busiest day = %s, want %s", got.BusiestDay.Date, local)
	}

	// West of UTC the same instant falls on the previous day, and the range
	// has to follow the reader there too.
	west := time.FixedZone("west", -8*60*60)
	if got := Build(ss, time.Date(2026, 7, 5, 12, 0, 0, 0, west)); !strings.HasPrefix(got.DateRange.Start, "2026-07-01") {
		t.Errorf("west of UTC: range starts %s, want 2026-07-01", got.DateRange.Start)
	}
}
