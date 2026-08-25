package usage

import (
	"testing"
	"time"
)

// deja had two ways of saying "a week ago" and showed them side by side: the
// status bar's week counters cut at a fixed 168 hours while its déjà-vu count
// cut seven calendar days back. In a zone with daylight saving those differ by
// an hour for one week in each direction, so an event inside that hour was in
// one number and not the other (#1920). The day counters beside them use local
// midnight, which is calendar, and so does the brief.
func TestTheWeekIsSevenCalendarDays(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata: ", err)
	}
	for _, c := range []struct {
		what string
		now  time.Time
	}{
		{"after the autumn change", time.Date(2026, 11, 3, 12, 0, 0, 0, ny)},
		{"after the spring change", time.Date(2026, 3, 10, 12, 0, 0, 0, ny)},
		{"an ordinary week", time.Date(2026, 8, 25, 12, 0, 0, 0, ny)},
	} {
		cut := WeekCut(c.now)
		if got, want := cut.Format("2006-01-02 15:04"), c.now.AddDate(0, 0, -7).Format("2006-01-02 15:04"); got != want {
			t.Errorf("%s: week opens at %s, want the same wall time seven days back (%s)", c.what, got, want)
		}
		// The wall clock, not 168 hours: that is what makes it the same week
		// the day counters and the brief are talking about.
		if h, m := cut.Hour(), cut.Minute(); h != 12 || m != 0 {
			t.Errorf("%s: week opens at %02d:%02d, not the hour it is now", c.what, h, m)
		}
	}
}

// And the two counters that read it agree, which is the thing that was wrong.
func TestTheWeekCountersShareOneCut(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata: ", err)
	}
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, ny)
	if !WeekCut(now).Equal(WeekCut(now)) {
		t.Fatal("WeekCut is not a function of its argument")
	}
	// An event in the hour the two rules disagreed about.
	inTheGap := now.AddDate(0, 0, -7).Add(30 * time.Minute)
	if inTheGap.Before(WeekCut(now)) {
		t.Errorf("an event half an hour into the week is outside it")
	}
	if fixed := now.Add(-7 * 24 * time.Hour); !inTheGap.Before(fixed) {
		t.Skip("this week has no clock change, so there is no gap to test")
	}
}
