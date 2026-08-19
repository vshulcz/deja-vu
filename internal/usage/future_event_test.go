package usage

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func appendAt(t *testing.T, dir string, at time.Time, kind string, bytes int) {
	t.Helper()
	f, err := os.OpenFile(Path(dir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	b, err := json.Marshal(Event{Time: at.UTC(), Kind: kind, Bytes: bytes, Sessions: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

// The counters are windows on the recent past, and an event dated ahead of the
// window sits inside every one of them. A clock that ran fast before it was
// corrected leaves one behind, and the rotation that bounds the log keeps
// whatever is newer than its cutoff — so it was counted in "today" every day
// until its own date arrived.
func TestCountersIgnoreAnEventFromTheFuture(t *testing.T) {
	dir := t.TempDir()
	Record(dir, KindRecall, 900)
	appendAt(t, dir, time.Now().AddDate(1, 0, 0), KindRecall, 4000)

	if r, b, _ := TodayWithInjections(dir); r != 1 || b != 900 {
		t.Errorf("today counted the future event: %d recalls, %d bytes", r, b)
	}
	if r, b, _, _ := Week(dir); r != 1 || b != 900 {
		t.Errorf("this week counted the future event: %d recalls, %d bytes", r, b)
	}
}

func TestDejaVuWeekIgnoresTheFuture(t *testing.T) {
	dir := t.TempDir()
	appendAt(t, dir, time.Now().Add(-time.Hour), KindDejaVu, 400)
	appendAt(t, dir, time.Now().AddDate(0, 1, 0), KindDejaVu, 400)
	if n := DejaVuWeek(dir); n != 1 {
		t.Errorf("DejaVuWeek = %d, want 1", n)
	}
}

func TestTodayRawIgnoresTheFuture(t *testing.T) {
	dir := t.TempDir()
	RecordResultRaw(dir, KindRecall, 900, 1, false, 5000)
	f, err := os.OpenFile(Path(dir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(Event{Time: time.Now().UTC().AddDate(1, 0, 0), Kind: KindRecall, Bytes: 4000, RawBytes: 90000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if got := TodayRaw(dir); got != 5000 {
		t.Errorf("TodayRaw = %d, want 5000", got)
	}
}

// Ordinary skew is not a false event: something stamped later today still
// counts, and so does the last moment of the day.
func TestOrdinarySkewStillCounts(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	endOfDay := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, now.Location())
	appendAt(t, dir, endOfDay, KindRecall, 700)
	if r, _, _ := TodayWithInjections(dir); r != 1 {
		t.Errorf("an event stamped later today was dropped: %d recalls", r)
	}
	if r, _, _, _ := Week(dir); r != 1 {
		t.Errorf("this week dropped it too: %d recalls", r)
	}
}

// The window is half-open: an event stamped at the midnight that ends today
// belongs to tomorrow, and one a microsecond before it belongs to today.
func TestTheDayBoundaryIsHalfOpen(t *testing.T) {
	now := time.Now()
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1)

	inside := t.TempDir()
	appendAt(t, inside, end.Add(-time.Microsecond), KindRecall, 700)
	if r, _, _ := TodayWithInjections(inside); r != 1 {
		t.Errorf("the last instant of today was dropped: %d recalls", r)
	}

	outside := t.TempDir()
	appendAt(t, outside, end, KindRecall, 700)
	if r, _, _ := TodayWithInjections(outside); r != 0 {
		t.Errorf("tomorrow's first instant counted as today: %d recalls", r)
	}
}

// Totals still report what the log holds: the guard is on the windows, not on
// the record.
func TestTotalsStillSeeTheFutureEvent(t *testing.T) {
	dir := t.TempDir()
	Record(dir, KindRecall, 900)
	appendAt(t, dir, time.Now().AddDate(1, 0, 0), KindRecall, 4000)
	if got := Totals(dir).Recalls; got != 2 {
		t.Errorf("Totals hid a recorded event: %d, want 2", got)
	}
}
