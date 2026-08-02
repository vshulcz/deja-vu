package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A status stamped ahead of the clock made time.Since negative, so it never
// went stale: every surface said "indexing your history" with no build running
// (#889).
func TestWarmupStatusAheadOfTheClockIsNotBelievedForever(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(offset time.Duration) {
		st := warmupStatus{Phase: "reading sessions", Total: 10, Done: 4,
			Started: time.Now().UnixNano(), Updated: time.Now().Add(offset).UnixNano()}
		b, err := json.Marshal(st)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(warmupStatusPath(dir), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(0)
	if readWarmupStatus(dir) == nil {
		t.Fatal("a fresh status was dropped")
	}
	// A few seconds ahead is ordinary skew between a parent and its detached
	// child, and still a live build.
	write(5 * time.Second)
	if readWarmupStatus(dir) == nil {
		t.Error("a status five seconds ahead was dropped")
	}
	// A day ahead is not a build in flight.
	write(24 * time.Hour)
	if st := readWarmupStatus(dir); st != nil {
		t.Errorf("a status stamped a day ahead was believed: %+v", st)
	}
	// And the old direction still holds.
	write(-time.Hour)
	if st := readWarmupStatus(dir); st != nil {
		t.Errorf("an hour-old status was believed: %+v", st)
	}
}
