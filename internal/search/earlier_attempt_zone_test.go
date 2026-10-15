package search

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// #5053: the "earlier attempt" day was printed in the zone the newer session's
// time carried, next to a header printed in the reader's. Fourteen hours east
// of a session updated at 23:45 UTC, both must say the next day.
func TestEarlierAttemptDayIsTheReadersDay(t *testing.T) {
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	time.Local = time.FixedZone("kiritimati", 14*3600)

	newerAt := time.Date(2026, 7, 20, 23, 45, 0, 0, time.UTC)
	old := stalenessSession("old1", "api", "connection pool exhausted under load, pgx acquire leak", newerAt.AddDate(0, 0, -10))
	fresh := stalenessSession("new1", "api", "connection pool exhausted under load fixed with acquire timeout", newerAt)
	hits, err := Run([]model.Session{old, fresh}, Options{Query: "connection pool exhausted", All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Session.ID == "old1" && h.Superseded != "2026-07-21" {
			t.Errorf("search: earlier attempt day %q, want the reader's 2026-07-21", h.Superseded)
		}
	}
	if got := EarlierAttempts([]model.Session{old, fresh})["old1"]; got != "2026-07-21" {
		t.Errorf("recall: earlier attempt day %q, want the reader's 2026-07-21", got)
	}
}
