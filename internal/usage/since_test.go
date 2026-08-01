package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The log is rewritten past 1MB keeping the last 14 days, so a count with no
// period attached reads as a lifetime total and then falls by orders of
// magnitude when that happens (#763).
func TestTotalsReportsTheOldestEventItCounted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	p := Path(dir)
	old := time.Now().UTC().AddDate(0, 0, -30)
	mid := time.Now().UTC().AddDate(0, 0, -3)
	var lines []byte
	for _, at := range []time.Time{mid, old, mid} {
		b, err := json.Marshal(Event{Time: at, Kind: KindRecall, Bytes: 100, Sessions: 1})
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, append(b, '\n')...)
	}
	if err := os.WriteFile(p, lines, 0o600); err != nil {
		t.Fatal(err)
	}
	got := Totals(dir)
	if got.Recalls != 3 {
		t.Fatalf("recalls = %d", got.Recalls)
	}
	// The oldest, not the first line: a log is appended to, but rotation and
	// clock changes can leave it out of order.
	if !got.Since.Equal(old) {
		t.Errorf("since = %v, want %v", got.Since, old)
	}

	// An empty log has no period to report.
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Totals(dir); !got.Since.IsZero() || got.Recalls != 0 {
		t.Errorf("empty log: since=%v recalls=%d", got.Since, got.Recalls)
	}
}
