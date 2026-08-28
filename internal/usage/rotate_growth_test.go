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
//
// Best of three on each side, because one loop is about six milliseconds and a
// runner busy with the rest of the suite stretches that by twenty: measured,
// one sample of each swung between 0.09 and 2.25 while the machine was loaded,
// and this test has failed once that way and passed alone. The minimum of
// several runs is the sample that was not interrupted.
func TestAWriteDoesNotPayForTheWholeLog(t *testing.T) {
	if testing.Short() {
		t.Skip("timing")
	}
	best := func(prepare func(dir string)) time.Duration {
		fastest := time.Duration(1<<62 - 1)
		for round := 0; round < 3; round++ {
			dir := t.TempDir()
			prepare(dir)
			start := time.Now()
			for i := 0; i < 200; i++ {
				RecordResultRaw(dir, KindRecall, 900, 2, false, 9000)
			}
			if took := time.Since(start); took < fastest {
				fastest = took
			}
		}
		return fastest
	}
	base := best(func(string) {})
	full := best(func(dir string) {
		for i := 0; i < 12000; i++ {
			RecordResultRaw(dir, KindRecall, 900, 2, false, 9000)
		}
	})

	// Both sides have to be worth measuring, or the ratio below is one piece
	// of noise divided by another.
	// Low enough that a machine several times faster than this one still
	// clears it — two hundred writes take about 6 ms here — and high enough
	// that a sample of pure noise does not become a ratio.
	if base < 200*time.Microsecond {
		t.Fatalf("two hundred writes to a fresh log took %v, which is too cheap to compare against", base)
	}
	// Ten times is far below the 135× this same test measures on the branch
	// before the fix, and far above the noise between two runs of one loop.
	if full > base*10 {
		t.Errorf("writing to a full log took %v against %v on a fresh one", full, base)
	}
	t.Logf("fresh %v, full %v", base, full)
}
