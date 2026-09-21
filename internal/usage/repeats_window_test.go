package usage

import (
	"path/filepath"
	"testing"
	"time"
)

// A doubling that has been fixed is history. Reading the whole log kept the
// row naming the morning before the install that collapsed the entries, with a
// remedy already applied (#3697).
func TestRepeatedInjectionsIgnoresWhatIsOlderThanTheWindow(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	// A repeat is two events in the same second (repeats.go), so the pairs below
	// have to sit inside one, not straddle its edge: from a bare time.Now() the
	// run that starts in the last millisecond of a second saw no repeats at all
	// (#3849).
	fresh := time.Now().Truncate(time.Second).Add(100 * time.Millisecond)
	old := fresh.Add(-48 * time.Hour)
	writeEvents(t, dir,
		Event{Time: old, Kind: KindDejaVu, Bytes: 800, Into: "s1"},
		Event{Time: old.Add(time.Millisecond), Kind: KindDejaVu, Bytes: 800, Into: "s1"},
		Event{Time: fresh, Kind: KindDejaVu, Bytes: 800, Into: "s2"},
		Event{Time: fresh.Add(time.Millisecond), Kind: KindDejaVu, Bytes: 800, Into: "s2"},
	)
	if got := RepeatedInjections(dir, time.Time{}); len(got) != 2 {
		t.Fatalf("the log holds two repeats to bound: %+v", got)
	}
	got := RepeatedInjections(dir, time.Now().Add(-time.Hour))
	if len(got) != 1 {
		t.Fatalf("a one-hour window should leave one repeat: %+v", got)
	}
	if got[0].Into != "s2" {
		t.Errorf("the window kept the wrong one: %+v", got)
	}
}
