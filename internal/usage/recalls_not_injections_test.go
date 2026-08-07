package usage

import (
	"testing"
	"time"
)

// `deja stats` prints "Recalls served" and "Injections" as two lines, and the
// first used to include the second: a log of two agent recalls and three
// session-start injections reported 5 and 3, while `deja stats --impact`
// reported 2 for the same events.
func TestTotalsCountsRecallsApartFromInjections(t *testing.T) {
	dir := usageDir(t)
	now := time.Now()
	appendEventForTest(t, dir, Event{Kind: KindRecall, Time: now, Bytes: 100, Sessions: 1})
	appendEventForTest(t, dir, Event{Kind: KindContext, Time: now, Bytes: 100, Sessions: 1})
	appendEventForTest(t, dir, Event{Kind: KindHook, Time: now, Bytes: 300, Sessions: 3})
	appendEventForTest(t, dir, Event{Kind: KindHook, Time: now, Bytes: 300, Sessions: 3})
	appendEventForTest(t, dir, Event{Kind: KindDejaVu, Time: now, Bytes: 300, Sessions: 3})

	got := Totals(dir)
	if got.Recalls != 2 {
		t.Errorf("recalls served = %d, want 2 — the three injections are counted on their own line", got.Recalls)
	}
	if got.Injections != 3 {
		t.Errorf("injections = %d, want 3", got.Injections)
	}
	if got.Recalls+got.Injections != 5 {
		t.Errorf("recalls+injections = %d, want the 5 logged events", got.Recalls+got.Injections)
	}
}

// The empty-result rate is a share of the agent recalls that returned nothing.
// Its denominator used to be Recalls minus Injections, which only worked while
// Recalls carried both.
func TestEmptyResultRateIsOverRecallsOnly(t *testing.T) {
	dir := usageDir(t)
	now := time.Now()
	appendEventForTest(t, dir, Event{Kind: KindRecall, Time: now, Bytes: 100, Sessions: 1})
	appendEventForTest(t, dir, Event{Kind: KindRecall, Time: now, Empty: true})
	appendEventForTest(t, dir, Event{Kind: KindHook, Time: now, Bytes: 300, Sessions: 3})

	if got := Totals(dir).EmptyResultRate; got != 0.5 {
		t.Errorf("empty result rate = %v, want 0.5 (1 of 2 recalls)", got)
	}
}
