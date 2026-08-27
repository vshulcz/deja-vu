package main

import (
	"os"
	"strings"
	"testing"
)

// The line renders on every prompt, and it read the whole usage log twice to do
// it — 16 ms on a 1.2 MB log, and two passes that can straddle a write and
// print two numbers that were never true together. The invariant is about the
// source: the status line asks the log once (#2224).
func TestTheStatusLineReadsTheUsageLogOnce(t *testing.T) {
	src, err := os.ReadFile("statusline.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"usage.TodayDemand(", "usage.Week(", "usage.TodayRaw("} {
		if strings.Contains(string(src), gone) {
			t.Errorf("statusline.go still calls %s — each is a pass over the log, and the line takes two", gone)
		}
	}
	if !strings.Contains(string(src), "usage.StatusCounters(") {
		t.Error("statusline.go no longer reads the counters through the single pass")
	}
}
