package usage

import (
	"testing"
	"time"
)

// A log over the size trigger because it is busy rather than old drops
// nothing, and #1900 stopped rewriting it for that reason. The read that
// decides it stayed: every event paid for the whole file, 6.4 ms against 28 µs
// on a fresh log and climbing (#1972). What one read found now answers for the
// next, so a process that records more than once pays it once.
//
// A ratio rather than a duration, so it means the same thing on a slow runner.
func TestAWriteDoesNotPayForTheWholeLog(t *testing.T) {
	if testing.Short() {
		t.Skip("timing")
	}
	fresh := t.TempDir()
	start := time.Now()
	for i := 0; i < 200; i++ {
		RecordResultRaw(fresh, KindRecall, 900, 2, false, 9000)
	}
	base := time.Since(start)

	big := t.TempDir()
	for i := 0; i < 12000; i++ {
		RecordResultRaw(big, KindRecall, 900, 2, false, 9000)
	}
	start = time.Now()
	for i := 0; i < 200; i++ {
		RecordResultRaw(big, KindRecall, 900, 2, false, 9000)
	}
	full := time.Since(start)

	// Ten times is far below the 135× this same test measures on the branch
	// before the fix, and far above the noise between two runs of one loop.
	if full > base*10 {
		t.Errorf("writing to a full log took %v against %v on a fresh one", full, base)
	}
}
