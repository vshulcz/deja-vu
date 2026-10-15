package sources

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #5053: aider writes its wall clock with no zone, and the header was read in
// the machine's zone, so after a zone change the same file gave other instants
// and an index kept from before it disagreed with a rebuild by up to a day.
func TestAiderHeaderReadsTheSameInEveryZone(t *testing.T) {
	path := filepath.Join(t.TempDir(), aiderHistoryName)
	body := aiderSessionMark + " 2026-07-20 23:45:00\n\n#### why does the pool run dry\n\nthe cap is too low\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	var starts []time.Time
	var ids []string
	for _, zone := range []*time.Location{time.FixedZone("pago", -11*3600), time.FixedZone("kiritimati", 14*3600)} {
		time.Local = zone
		ss, err := ParseAiderFile(path)
		if err != nil || len(ss) != 1 {
			t.Fatalf("parse: %d sessions, %v", len(ss), err)
		}
		starts = append(starts, ss[0].Started)
		ids = append(ids, ss[0].ID)
	}
	if !starts[0].Equal(starts[1]) || ids[0] != ids[1] {
		t.Fatalf("the same header read as %v (%s) and %v (%s)", starts[0], ids[0], starts[1], ids[1])
	}
	if want := time.Date(2026, 7, 20, 23, 45, 0, 0, time.UTC); !starts[0].Equal(want) {
		t.Fatalf("started %v, want the wall clock read as UTC, %v", starts[0], want)
	}
}
