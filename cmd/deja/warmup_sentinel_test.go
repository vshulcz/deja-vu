package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// A sentinel stamped ahead of the clock (a clock stepped back, an index dir
// copied from another machine) is stale, not a build about to start: taking
// it for fresh kept recall saying "indexing" while nothing was built.
func TestWarmupSentinelAheadOfTheClockIsStale(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	put := func(at time.Time) int64 {
		stamp := at.UnixNano()
		if err := os.WriteFile(filepath.Join(dir, "warmup.sentinel"), []byte(strconv.FormatInt(stamp, 10)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return stamp
	}
	now := time.Now()

	put(now)
	if !warmupJustRequested(dir) {
		t.Fatal("a sentinel written now does not read as just requested")
	}
	ahead := put(now.Add(6 * time.Hour))
	if warmupJustRequested(dir) {
		t.Error("a sentinel six hours ahead reads as just requested")
	}
	if !warmupLooksDead(dir, now, ahead) {
		t.Error("a sentinel six hours ahead with no build behind it does not read as dead")
	}
	behind := put(now.Add(-6 * time.Hour))
	if warmupJustRequested(dir) || !warmupLooksDead(dir, now, behind) {
		t.Error("a sentinel six hours old reads as live")
	}
}
