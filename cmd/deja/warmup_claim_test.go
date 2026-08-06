package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The warmup claim is the O_EXCL create; the stamp inside the file lands a
// moment later. A second hook reading the sentinel in that window found it
// empty, called it stale, deleted the other hook's claim and spawned a build
// of its own — two rebuilds over one index directory whenever two projects
// started together (#1065).
func TestWarmupClaimInFlightIsNotStolen(t *testing.T) {
	sentinelOfAge := func(t *testing.T, age time.Duration) (string, *int) {
		t.Helper()
		tmp := hermeticEnv(t)
		// requestWarmup does nothing inside a warmup child; this is the hook.
		t.Setenv("DEJA_WARMUP_SENTINEL", "")
		dir := filepath.Join(tmp, "idx")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "warmup.sentinel")
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-age)
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
		spawns := 0
		old := spawnWarmup
		spawnWarmup = func(_, _ string) error { spawns++; return nil }
		t.Cleanup(func() { spawnWarmup = old })
		return dir, &spawns
	}

	t.Run("a claim being written right now", func(t *testing.T) {
		dir, spawns := sentinelOfAge(t, 0)
		requestWarmup(dir)
		if *spawns != 0 {
			t.Errorf("spawned %d warmups over a claim made a moment ago, want 0", *spawns)
		}
	})

	// The empty sentinel still has to expire, or a warmup killed between the
	// create and the write would block every later one for the retry window.
	t.Run("a claim nobody ever finished", func(t *testing.T) {
		dir, spawns := sentinelOfAge(t, warmupDeadAfter+time.Minute)
		requestWarmup(dir)
		if *spawns != 1 {
			t.Errorf("spawned %d warmups over an abandoned claim, want 1", *spawns)
		}
	})
}
