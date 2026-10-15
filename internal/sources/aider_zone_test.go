package sources

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// aider writes its local wall clock with no zone. Read as UTC, every aider
// time showed shifted by the user's offset (#5053): 23:45 here must stay 23:45.
func TestAiderHeaderReadsAsLocalWallTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), aiderHistoryName)
	body := aiderSessionMark + " 2026-07-20 23:45:00\n\n#### why does the pool run dry\n\nthe cap is too low\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	for _, zone := range []*time.Location{time.FixedZone("pago", -11*3600), time.FixedZone("kiritimati", 14*3600)} {
		time.Local = zone
		ss, err := ParseAiderFile(path)
		if err != nil || len(ss) != 1 {
			t.Fatalf("parse: %d sessions, %v", len(ss), err)
		}
		got := ss[0].Started
		if want := time.Date(2026, 7, 20, 23, 45, 0, 0, zone); !got.Equal(want) {
			t.Errorf("%s: started %v, want the wall clock in this zone, %v", zone, got, want)
		}
		if wall := got.In(time.Local).Format("2006-01-02 15:04:05"); wall != "2026-07-20 23:45:00" {
			t.Errorf("%s: shows as %s, want 2026-07-20 23:45:00", zone, wall)
		}
	}
}
