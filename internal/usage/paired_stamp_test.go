package usage

import (
	"path/filepath"
	"testing"
)

// One injection writes an event and a digest snapshot. They carried separate
// time.Now() calls, so the two logs disagreed by microseconds about when the
// same thing happened and nothing could join them (#2294).
func TestAnInjectionCarriesOneStampInBothLogs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")

	RecordDigestPolicyInto(dir, "dejavu", "the digest text", "agent-1", 1, 63, "local+imported")

	events := Events(dir, 0)
	snaps := Snapshots(dir, 0)
	// The premise: exactly one of each was written.
	if len(events) != 1 || len(snaps) != 1 {
		t.Fatalf("%d events and %d snapshots, want one of each", len(events), len(snaps))
	}
	if !events[0].Time.Equal(snaps[0].Time) {
		t.Errorf("the same injection is stamped %s in the event log and %s in the snapshot log",
			events[0].Time.Format("15:04:05.000000"), snaps[0].Time.Format("15:04:05.000000"))
	}

	// The other pair writer has the same shape.
	dir2 := filepath.Join(t.TempDir(), "index.db")
	RecordDigestInto(dir2, "recall", "another digest", "agent-2", 2, 99, []string{"gateway"}, "s1", "s2")
	events, snaps = Events(dir2, 0), Snapshots(dir2, 0)
	if len(events) != 1 || len(snaps) != 1 {
		t.Fatalf("%d events and %d snapshots, want one of each", len(events), len(snaps))
	}
	if !events[0].Time.Equal(snaps[0].Time) {
		t.Errorf("RecordDigestInto stamps %s and %s",
			events[0].Time.Format("15:04:05.000000"), snaps[0].Time.Format("15:04:05.000000"))
	}
}
