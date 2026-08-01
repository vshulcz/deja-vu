package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// justAfterMidnight returns a moment that is today for every hour the suite
// might run at: local midnight plus a minute, or now if the run is inside that
// first minute.
func justAfterMidnight(now time.Time) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), 0, 1, 0, 0, now.Location())
	if t.After(now) {
		return now
	}
	return t
}

// The counters tested Updated.After(midnight) with no upper bound, so anything
// ahead of the clock was today's work, this week's, and the newest thing in the
// store — permanently. On a 10k-session store that read "today 2222" (#696).
func TestOverviewExcludesSessionsFromTheFuture(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	// Anchored inside today rather than measured backwards from now: two hours
	// ago is yesterday when the suite runs shortly after midnight, and
	// SessionsToday counts from local midnight (#837).
	today := justAfterMidnight(now)
	sessions := map[string]SessionMeta{
		"claude:today":     {ID: "today", Harness: "claude", Updated: today},
		"claude:yesterday": {ID: "yesterday", Harness: "claude", Updated: now.AddDate(0, 0, -1)},
		"claude:lastmonth": {ID: "lastmonth", Harness: "claude", Updated: now.AddDate(0, -1, 0)},
		// Just inside the week, so narrowing the window shows up too.
		"claude:sixdays": {ID: "sixdays", Harness: "claude", Updated: now.Add(-6*24*time.Hour - 12*time.Hour)},
		// Just outside the week, so widening the window shows up.
		"claude:eightdays": {ID: "eightdays", Harness: "claude", Updated: now.Add(-7*24*time.Hour - 12*time.Hour)},
		"claude:soon":      {ID: "soon", Harness: "claude", Updated: now.Add(3 * time.Hour)},
		"claude:nextweek":  {ID: "nextweek", Harness: "claude", Updated: now.AddDate(0, 0, 6)},
		"claude:nextyear":  {ID: "nextyear", Harness: "claude", Updated: now.AddDate(1, 0, 0)},
	}
	if err := writeManifest(dir, Manifest{Version: version, Files: map[string]FileState{}, Sessions: sessions}); err != nil {
		t.Fatal(err)
	}
	ov, err := Overview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ov.Sessions != 8 {
		t.Errorf("sessions = %d, want 8 — the future ones still exist", ov.Sessions)
	}
	if ov.SessionsToday != 1 {
		t.Errorf("today = %d, want 1", ov.SessionsToday)
	}
	if ov.SessionsWeek != 3 {
		t.Errorf("this week = %d, want 3", ov.SessionsWeek)
	}
	// A session three hours from now is still ahead of the clock, even though
	// it falls on today's date.
	if ov.Future != 3 {
		t.Errorf("future = %d, want 3", ov.Future)
	}
}

func TestOverviewWithNoFutureSessionsSaysNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	sessions := map[string]SessionMeta{
		"claude:a": {ID: "a", Harness: "claude", Updated: justAfterMidnight(now)},
		"claude:b": {ID: "b", Harness: "claude", Updated: now.AddDate(0, 0, -3)},
	}
	if err := writeManifest(dir, Manifest{Version: version, Files: map[string]FileState{}, Sessions: sessions}); err != nil {
		t.Fatal(err)
	}
	ov, err := Overview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ov.Future != 0 || ov.SessionsToday != 1 || ov.SessionsWeek != 2 {
		t.Errorf("ov = %+v", ov)
	}
}
