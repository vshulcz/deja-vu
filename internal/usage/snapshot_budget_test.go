package usage

import (
	"strings"
	"testing"
)

// The rotation keeps a fixed count, so the size of one record is what decides
// whether the file stays under its threshold — and the budgets that set that
// size live in packages this one cannot import. `RecordRoom` is the line
// between them; `cmd/deja` holds the budgets to it.
//
// Here: twenty records at the largest budget today fit, which is what the line
// is for. Above it the file sits over the threshold for good and half of two
// concurrent injections is rewritten away (#1971).
func TestTwentyOfTheLargestAnswerStillFit(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("b", 8192)
	for i := 0; i < snapshotsToKeep; i++ {
		RecordDigestPolicy(dir, KindBlame, big, 1, 0, "local-only")
	}
	if got := len(Snapshots(dir, 0)); got != snapshotsToKeep {
		t.Errorf("the log holds %d of %d records at the largest budget", got, snapshotsToKeep)
	}
}
