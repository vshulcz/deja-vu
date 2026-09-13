package main

import (
	"runtime"
	"testing"
)

// The detached warmup is index work nobody asked for, started at the moment
// someone is working — a compaction asks for one mid-session. It used to run
// like a foreground rebuild: every core the machine has, at the same scheduling
// priority as the agent waiting on the other side of the terminal (#3500).
//
// The ambient GOMAXPROCS is set explicitly here rather than read: another test
// in this package can leave it at half the cores, and then "did the cap lower
// it" compares two equal numbers and passes for the wrong reason — which is how
// the first version of this test passed alone and failed in the suite.
func TestAWarmupTakesLessThanAForegroundRun(t *testing.T) {
	if runtime.NumCPU() < 2 {
		t.Skip("one core: there is nothing to cap")
	}
	was := runtime.GOMAXPROCS(0)
	t.Cleanup(func() { runtime.GOMAXPROCS(was) })
	all := runtime.NumCPU()

	// Without the sentinel this is an ordinary command and nothing changes.
	runtime.GOMAXPROCS(all)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	t.Setenv("GOMAXPROCS", "")
	takeWarmupBudget()
	if now := runtime.GOMAXPROCS(0); now != all {
		t.Errorf("an ordinary run was capped at %d of %d cores", now, all)
	}

	// With it, half the cores at most, and never more than the eight a bucket
	// write already uses.
	runtime.GOMAXPROCS(all)
	t.Setenv("DEJA_WARMUP_SENTINEL", "/tmp/whatever.sentinel")
	takeWarmupBudget()
	now := runtime.GOMAXPROCS(0)
	if now >= all {
		t.Errorf("the warmup kept %d of %d cores", now, all)
	}
	if now > 8 {
		t.Errorf("the warmup kept %d cores, more than the bucket writers use", now)
	}
	if now < 1 {
		t.Errorf("the warmup was left with %d cores", now)
	}

	// An explicit GOMAXPROCS is the operator's, not ours to override.
	runtime.GOMAXPROCS(all)
	t.Setenv("GOMAXPROCS", "3")
	takeWarmupBudget()
	if now := runtime.GOMAXPROCS(0); now != all {
		t.Errorf("GOMAXPROCS=3 from the environment was overridden to %d", now)
	}
}
