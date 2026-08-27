package sources

import (
	"encoding/json"
	"math"
	"strconv"
	"testing"
	"time"
)

// unixGuess is the front door for every numeric timestamp deja reads, and it
// knew two units: anything larger than milliseconds was read as milliseconds,
// so a microsecond store was dated to the year 58136 and a nanosecond one to
// the year 56 million. A session dated in the future takes the top of every
// recency surface and keeps it (#2063), so one such store pins them all (#2102).
func TestAStampIsReadInWhicheverUnitItWasWritten(t *testing.T) {
	when := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	for _, u := range []struct {
		name string
		n    int64
	}{
		{"seconds", when.Unix()},
		{"milliseconds", when.UnixMilli()},
		{"microseconds", when.UnixMicro()},
		{"nanoseconds", when.UnixNano()},
	} {
		got := parseTimeAny(json.Number(strconv.FormatInt(u.n, 10)))
		if !got.UTC().Equal(when) {
			t.Errorf("%s (%d) read as %s, want %s", u.name, u.n, got.UTC().Format(time.RFC3339), when.Format(time.RFC3339))
		}
	}
}

// The bands are judged by magnitude, so the edges are worth pinning: a value
// deja cannot place is better read as the unit that puts it in a plausible year
// than as one that puts it past the year 5000.
func TestTheUnitBandsHoldAtTheirEdges(t *testing.T) {
	for _, c := range []struct {
		name string
		n    int64
		want time.Time
	}{
		{"an old seconds stamp", 915148800, time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"an old millisecond stamp", 915148800000, time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"a 2001 millisecond stamp", 1_000_000_000_000, time.Date(2001, 9, 9, 1, 46, 40, 0, time.UTC)},
		{"zero is no stamp", 0, time.Time{}},
		{"a negative is no stamp", -1, time.Time{}},
		// The largest thing an int64 can carry: nanoseconds run out in 2262,
		// and the call takes it rather than panicking.
		{"the largest int64", math.MaxInt64, time.Unix(0, math.MaxInt64)},
	} {
		got := parseTimeAny(json.Number(strconv.FormatInt(c.n, 10)))
		if !got.UTC().Equal(c.want.UTC()) {
			t.Errorf("%s (%d) read as %s, want %s", c.name, c.n, got.UTC().Format(time.RFC3339), c.want.UTC().Format(time.RFC3339))
		}
	}
}
