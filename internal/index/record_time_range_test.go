package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// records.bin stores a message time as a nanosecond count, which only spans
// 1678-2262. A transcript stamped 2999 wrapped to 1829 and one stamped 1200
// wrapped to 1784: both come back as ordinary-looking dates, so `deja stats`
// reported a Range starting two centuries before the store's oldest session
// and nothing looked wrong. Out of range must stay out of range.
func TestOutOfRangeMessageTimesDoNotWrap(t *testing.T) {
	cases := []struct {
		name  string
		when  time.Time
		after bool // true: must land at or after the far future edge
	}{
		{"far future", time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC), true},
		{"before the epoch window", time.Date(1200, 5, 6, 0, 0, 0, 0, time.UTC), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "records.bin")
			f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			rw, err := newRecordWriter(f, newRecordTables())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := rw.write(Record{Key: "claude:s1", SourcePath: "/tmp/s1.jsonl", Role: "user", Text: "stamped", Time: c.when}); err != nil {
				t.Fatal(err)
			}
			if err := rw.Close(); err != nil {
				t.Fatal(err)
			}
			var got Record
			if err := eachRecord(p, newRecordTables(), func(r Record) { got = r }); err != nil {
				t.Fatal(err)
			}
			if got.Time.IsZero() {
				t.Fatalf("%v decoded as unstamped, which means the message has no date at all", c.when)
			}
			if c.after && got.Time.Before(maxRecordTime) {
				t.Fatalf("%v decoded as %v — a future stamp moved into the past", c.when, got.Time)
			}
			if !c.after && got.Time.After(minRecordTime) {
				t.Fatalf("%v decoded as %v — an ancient stamp moved forward", c.when, got.Time)
			}
		})
	}

	// An in-range stamp is untouched, and an unstamped record still reads as
	// unstamped: the clamp must not swallow either.
	when := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	if got := recordNanos(when); got != when.UnixNano() {
		t.Errorf("recordNanos rewrote an ordinary stamp: %d want %d", got, when.UnixNano())
	}
	if got := recordNanos(time.Time{}); got != zeroTimeUnixNano {
		t.Errorf("recordNanos lost the unstamped marker: %d want %d", got, zeroTimeUnixNano)
	}
}
