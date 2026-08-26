package usage

import (
	"strings"
	"testing"
)

// The rotation keeps a fixed count, so what keeps the file under its own
// threshold is the size of a record — and that comes from budgets in another
// package. Twenty of the largest writer today is about 164 kB against 512 kB:
//
//	recall / recall_context   4096 (2048 under a narrowing policy)
//	blame                     8192
//	resource                  8000
//	déjà vu                   1536
//	handoff                   6144
//	tool                       480
//
// Above `snapshotRotateAt / snapshotsToKeep` the file sits over the threshold
// for good, every write rotates, and half of two concurrent injections is
// rewritten away — measured in #1971, which is also where a rotation learned to
// drop the oldest until the rebuild fits. This is the other half: a budget that
// grows past this line silently puts the log back in that state, and the number
// to raise then is the threshold, not the budget.
func TestARecordThisLogCanHoldWithoutRotatingEveryTime(t *testing.T) {
	const perRecord = snapshotRotateAt / snapshotsToKeep
	if perRecord < 8192 {
		t.Fatalf("a record of the largest budgeted answer (8192) no longer fits: %d bytes each", perRecord)
	}

	// Twenty at the largest budget, and the file is still under the threshold.
	dir := t.TempDir()
	big := strings.Repeat("b", 8192)
	for i := 0; i < snapshotsToKeep; i++ {
		RecordDigestPolicy(dir, KindBlame, big, 1, 0, "local-only")
	}
	if got := len(Snapshots(dir, 0)); got != snapshotsToKeep {
		t.Errorf("the log holds %d of %d records at the largest budget", got, snapshotsToKeep)
	}
}

// And a record over that line does not take the log with it: the rotation drops
// the oldest until the rebuild fits, so the next write is not rotating again.
func TestALogOfOversizedRecordsStaysUnderTheThreshold(t *testing.T) {
	dir := t.TempDir()
	huge := strings.Repeat("h", 64<<10)
	for i := 0; i < snapshotsToKeep+4; i++ {
		RecordDigestPolicy(dir, KindResource, huge, 1, 0, "local-only")
	}
	got := Snapshots(dir, 0)
	if len(got) == 0 {
		t.Fatal("the log is empty")
	}
	if got[0].Digest != huge {
		t.Errorf("the newest record is %d bytes, not the one just written", len(got[0].Digest))
	}
}
