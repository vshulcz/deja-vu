package usage

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// The brief printed four figures and walked the log once per figure, and the
// week note walked it twice for two (#1576). Both read from one pass now, so
// what the pass reports has to be what the separate readers reported — and the
// two counters that had no field on StatusNumbers have to be right on their own
// terms, not merely consistent with a wrapper that now returns them.

func weekFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	today := func(ago time.Duration) time.Time {
		if at := now.Add(-ago); !at.Before(midnight) {
			return at
		}
		return midnight.Add(time.Second)
	}
	// Three déjà vu moments inside the week, of which one found nothing behind
	// it and is not a moment at all.
	appendEventForTest(t, dir, Event{Time: today(time.Hour), Kind: KindDejaVu, Sessions: 3})
	appendEventForTest(t, dir, Event{Time: now.Add(-48 * time.Hour), Kind: KindDejaVu, Sessions: 1})
	appendEventForTest(t, dir, Event{Time: today(2 * time.Hour), Kind: KindDejaVu, Sessions: 0})
	// One older than the week, which is not this week's news.
	appendEventForTest(t, dir, Event{Time: now.Add(-30 * 24 * time.Hour), Kind: KindDejaVu, Sessions: 4})
	// Two unprompted arrivals inside the week, and a recall beside them.
	appendEventForTest(t, dir, Event{Time: today(3 * time.Hour), Kind: KindHook, Bytes: 500, Sessions: 2})
	appendEventForTest(t, dir, Event{Time: now.Add(-24 * time.Hour), Kind: KindHook, Bytes: 700, Sessions: 1})
	appendEventForTest(t, dir, Event{Time: today(4 * time.Hour), Kind: KindRecall, Bytes: 900, Sessions: 2, RawBytes: 9000})
	return dir
}

func TestOnePassCountsThisWeeksDejaVuAndArrivals(t *testing.T) {
	dir := weekFixture(t)
	got := StatusCounters(dir)
	if got.DejaVuWeek != 2 {
		t.Errorf("déjà vu this week = %d, want 2 (the third had no sessions behind it, the fourth is older than the week)", got.DejaVuWeek)
	}
	// Five, not two: a déjà vu moment is an injected kind, so the three inside
	// the week are arrivals as well — including the one with no sessions
	// behind it, since the empty rule does not apply to an injection. This is
	// what Week() counted before, and the point of the field is to keep
	// counting it.
	if got.WeekInjections != 5 {
		t.Errorf("arrivals this week = %d, want 5", got.WeekInjections)
	}
	// Only the two hooks carried bytes; a déjà vu moment is a notice.
	if got.WeekInjectedBytes != 1200 {
		t.Errorf("arrival bytes this week = %d, want 1200", got.WeekInjectedBytes)
	}
	if got.WeekRecalls != 1 || got.WeekBytes != 900 {
		t.Errorf("week recalls = %d/%d, want 1/900", got.WeekRecalls, got.WeekBytes)
	}
}

// The readers the brief and the week note used to call are wrappers now. Each
// has to return its own figures — a wrapper wired to the wrong field would
// still compile and still look consistent with itself.
func TestTheWrappersReturnTheirOwnFigures(t *testing.T) {
	dir := weekFixture(t)
	got := StatusCounters(dir)

	if dv := DejaVuWeek(dir); dv != got.DejaVuWeek {
		t.Errorf("DejaVuWeek = %d, one pass says %d", dv, got.DejaVuWeek)
	}
	recalls, bytes, injected := TodayDemand(dir)
	if recalls != got.Recalls || bytes != got.Bytes || injected != got.Injected {
		t.Errorf("TodayDemand = %d/%d/%d, one pass says %d/%d/%d",
			recalls, bytes, injected, got.Recalls, got.Bytes, got.Injected)
	}
	wr, wb, wi, wib := Week(dir)
	if wr != got.WeekRecalls || wb != got.WeekBytes || wi != got.WeekInjections || wib != got.WeekInjectedBytes {
		t.Errorf("Week = %d/%d/%d/%d, one pass says %d/%d/%d/%d",
			wr, wb, wi, wib, got.WeekRecalls, got.WeekBytes, got.WeekInjections, got.WeekInjectedBytes)
	}
	if raw := TodayRaw(dir); raw != got.RawToday {
		t.Errorf("TodayRaw = %d, one pass says %d", raw, got.RawToday)
	}
	// Premise: none of this is zero, or agreement measures nothing.
	if got.Recalls == 0 || got.WeekRecalls == 0 || got.RawToday == 0 || got.DejaVuWeek == 0 || got.WeekInjections == 0 {
		t.Fatalf("the fixture produced %+v, so agreement measures nothing", got)
	}
}

// A log the size of a busy fortnight: the issue measured 3,162 lines / 324 KB
// on a real machine, and rotation keeps 14 days or 1 MB.
func benchLog(b *testing.B, lines int) string {
	b.Helper()
	dir := b.TempDir()
	f, err := os.OpenFile(Path(dir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	now := time.Now()
	kinds := []string{KindRecall, KindHook, KindContext, KindDejaVu}
	for i := range lines {
		// Spread over the retained window, so most lines fall outside the day
		// the counters keep — which is the work a pass does and throws away.
		e := Event{
			Time:     now.Add(-time.Duration(i) * 6 * time.Minute),
			Kind:     kinds[i%len(kinds)],
			Bytes:    100 + i%900,
			Sessions: 1 + i%3,
			RawBytes: int64(1000 + i%9000),
		}
		line, err := json.Marshal(e)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			b.Fatal(err)
		}
	}
	return dir
}

// Before: the four figures the brief prints, one reader each.
func BenchmarkBriefFiguresSeparatePasses(b *testing.B) {
	dir := benchLog(b, 3200)
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = TodayDemand(dir)
		_, _, _, _ = Week(dir)
		_ = DejaVuWeek(dir)
		_ = TodayRaw(dir)
	}
}

// After: the same four figures, one walk.
func BenchmarkBriefFiguresOnePass(b *testing.B) {
	dir := benchLog(b, 3200)
	b.ResetTimer()
	for b.Loop() {
		_ = StatusCounters(dir)
	}
}
