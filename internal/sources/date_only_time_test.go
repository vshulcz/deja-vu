package sources

import (
	"testing"
	"time"
)

// The layout list covers the pieces a store may leave out — a zone, seconds, a
// T where a space is — because losing one loses the whole stamp: the session
// sorts as "-", `deja last` never shows it and no `--since` window contains it.
// A date with no time at all was the one shape still missing.
func TestADateWithNoTimeKeepsItsDate(t *testing.T) {
	got := parseTimeAny("2026-09-06")
	if got.IsZero() {
		t.Fatal("a date-only stamp lost the whole date")
	}
	if y, m, d := got.Date(); y != 2026 || m != time.September || d != 6 {
		t.Errorf("parsed %s, want 2026-09-06", got.Format(time.RFC3339))
	}
	if h, min, s := got.Clock(); h != 0 || min != 0 || s != 0 {
		t.Errorf("a date with no time got a clock reading: %s", got.Format(time.RFC3339))
	}
	// And the shapes that are not a stamp at all still return zero, so a
	// session is not dated from a version string or a path fragment. A bare
	// number is left out of this list on purpose: the field is always a
	// timestamp, and a store that stringifies the epoch writes one.
	for _, in := range []string{"", "2026-09", "v1.2.3", "internal/index", "September 6"} {
		if got := parseTimeAny(in); !got.IsZero() {
			t.Errorf("%q was read as %s", in, got.Format(time.RFC3339))
		}
	}
}
