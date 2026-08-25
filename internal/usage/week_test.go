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

// The hour the two rules disagreed about, asserted rather than skipped past.
// After the autumn change the fixed-hours cut lands an hour later than the
// calendar one, so an event in between is inside this week by the rule deja
// keeps and outside it by the rule it dropped — which is the event that used to
// be in one counter and not the other.
func TestTheHourTheOldRuleWouldHaveMissed(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata: ", err)
	}
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, ny)
	fixed := now.Add(-7 * 24 * time.Hour)
	cut := WeekCut(now)
	if !cut.Before(fixed) {
		t.Fatalf("this week has no clock change in it: cut=%s fixed=%s", cut, fixed)
	}
	event := cut.Add(30 * time.Minute)
	if event.Before(cut) {
		t.Errorf("an event half an hour into the week is outside it")
	}
	if !event.Before(fixed) {
		t.Errorf("the event is not in the hour the two rules disagreed about: %s", event)
	}
}

// And in spring the disagreement runs the other way: the fixed-hours cut opens
// an hour early, so it counted an event from the week before.
func TestTheHourTheOldRuleWouldHaveAdded(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata: ", err)
	}
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, ny)
	fixed := now.Add(-7 * 24 * time.Hour)
	cut := WeekCut(now)
	if !fixed.Before(cut) {
		t.Fatalf("this week has no clock change in it: cut=%s fixed=%s", cut, fixed)
	}
	event := fixed.Add(30 * time.Minute)
	if !event.Before(cut) {
		t.Errorf("an event before this week's start is counted inside it: %s", event)
	}
}
