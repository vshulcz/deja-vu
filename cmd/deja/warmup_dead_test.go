package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// A warmup killed mid-build leaves its sentinel behind, and the sentinel is
// what stops the next hook from starting another. For ten minutes every hook
// then returned nothing, spawned nothing and said nothing (#875).
func TestAKilledWarmupIsRetriedWithoutWaitingOutTheWindow(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	dir := filepath.Join(tmp, "index.db")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(dir, "warmup.sentinel")

	spawned := 0
	saved := spawnWarmup
	spawnWarmup = func(exe, s string) error { spawned++; return nil }
	t.Cleanup(func() { spawnWarmup = saved })

	writeSentinel := func(age time.Duration) {
		stamp := time.Now().Add(-age).UnixNano()
		if err := os.WriteFile(sentinel, []byte(strconv.FormatInt(stamp, 10)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeStatus := func(age time.Duration) {
		st := warmupStatus{Phase: "reading sessions", Total: 10, Done: 1, Started: time.Now().UnixNano(), Updated: time.Now().Add(-age).UnixNano()}
		b, err := json.Marshal(st)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(warmupStatusPath(dir), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// A build that is running and reporting: no second warmup.
	writeSentinel(30 * time.Second)
	writeStatus(0)
	requestWarmup(dir)
	if spawned != 0 {
		t.Errorf("a live build was restarted %d time(s)", spawned)
	}

	// Same sentinel age, no status at all: still inside the grace period, so
	// a build that has not published its first report yet is left alone.
	if err := os.Remove(warmupStatusPath(dir)); err != nil {
		t.Fatal(err)
	}
	requestWarmup(dir)
	if spawned != 0 {
		t.Errorf("a build that had not reported yet was restarted %d time(s)", spawned)
	}

	// Sentinel older than the grace period and nothing reporting: dead.
	writeSentinel(3 * time.Minute)
	requestWarmup(dir)
	if spawned != 1 {
		t.Errorf("a dead warmup was retried %d time(s), want 1", spawned)
	}
	// The sentinel is rewritten, so the next hook does not spawn a third.
	b, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	stamp, err := strconv.ParseInt(string(b[:len(b)-1]), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if age := time.Since(time.Unix(0, stamp)); age > time.Minute {
		t.Errorf("sentinel was not refreshed: %v old", age)
	}
	requestWarmup(dir)
	if spawned != 1 {
		t.Errorf("the retry spawned again immediately: %d", spawned)
	}
}
