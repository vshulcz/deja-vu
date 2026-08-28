package usage

import (
	"testing"
	"time"
)

// One pass for the line that renders on every prompt. The numbers have to be
// the ones the separate readers gave, or this is a different status line
// (#2224).
func TestStatusCountersAgreeWithTheReadersTheyReplace(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	// Today, whatever the hour: "two hours ago" is yesterday between midnight
	// and two in the morning, and the counters this test is about cut at local
	// midnight — so the fixture would have had nothing in it and the test would
	// have said so at 00:24.
	today := func(ago time.Duration) time.Time {
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		if at := now.Add(-ago); !at.Before(midnight) {
			return at
		}
		return midnight.Add(time.Second)
	}
	appendEventForTest(t, dir, Event{Time: today(2 * time.Hour), Kind: KindRecall, Bytes: 900, Sessions: 2, RawBytes: 9000})
	appendEventForTest(t, dir, Event{Time: today(3 * time.Hour), Kind: KindContext, Bytes: 400, Sessions: 1, RawBytes: 4000})
	appendEventForTest(t, dir, Event{Time: today(1 * time.Hour), Kind: KindHook, Bytes: 600, Sessions: 1, RawBytes: 6000})
	// An empty recall serves nothing and must not count, an empty injection
	// carried the environment block and its bytes still do (#1962).
	appendEventForTest(t, dir, Event{Time: today(30 * time.Minute), Kind: KindRecall, Empty: true})
	appendEventForTest(t, dir, Event{Time: today(40 * time.Minute), Kind: KindHook, Bytes: 300, Empty: true})
	// Earlier in the week but not today.
	appendEventForTest(t, dir, Event{Time: now.Add(-72 * time.Hour), Kind: KindRecall, Bytes: 100, Sessions: 1, RawBytes: 1000})
	// Older than the week.
	appendEventForTest(t, dir, Event{Time: now.Add(-20 * 24 * time.Hour), Kind: KindRecall, Bytes: 5000, Sessions: 1, RawBytes: 50000})

	got := StatusCounters(dir)
	recalls, bytes, injected := TodayDemand(dir)
	if got.Recalls != recalls || got.Bytes != bytes || got.Injected != injected {
		t.Errorf("today: one pass says %d/%d/%d, TodayDemand says %d/%d/%d",
			got.Recalls, got.Bytes, got.Injected, recalls, bytes, injected)
	}
	wr, wb, _, _ := Week(dir)
	if got.WeekRecalls != wr || got.WeekBytes != wb {
		t.Errorf("week: one pass says %d/%d, Week says %d/%d", got.WeekRecalls, got.WeekBytes, wr, wb)
	}
	if raw := TodayRaw(dir); got.RawToday != raw {
		t.Errorf("raw: one pass says %d, TodayRaw says %d", got.RawToday, raw)
	}
	// The premise: these numbers are not all zero, or agreeing means nothing.
	if got.Recalls == 0 || got.Bytes == 0 || got.Injected == 0 || got.WeekRecalls == 0 || got.RawToday == 0 {
		t.Fatalf("the fixture produced %+v, so agreement measures nothing", got)
	}
}

// An empty log answers zeros rather than reading anything into them.
func TestStatusCountersOnAnEmptyLog(t *testing.T) {
	got := StatusCounters(t.TempDir())
	if got != (StatusNumbers{}) {
		t.Errorf("an empty log reports %+v", got)
	}
}
