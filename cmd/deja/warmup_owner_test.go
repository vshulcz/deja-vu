package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ownerTestEnv is a hermetic index directory and a stub for the detached
// spawn, as a hook sees them.
func ownerTestEnv(t *testing.T) (dir string, spawns *int) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("HERMES_HOME", filepath.Join(tmp, "hermes"))
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	t.Setenv("DEJA_WARMUP_CLAIM", "")
	dir = filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	n := 0
	saved := spawnWarmup
	spawnWarmup = func(_, _ string) error { n++; return nil }
	t.Cleanup(func() { spawnWarmup = saved })
	return dir, &n
}

// writeClaim writes a sentinel with a stamp age old and the given body after
// it, and dates the file the same, so the claim reads equally old to any
// reader.
func writeClaim(t *testing.T, path string, age time.Duration, rest string) int64 {
	t.Helper()
	when := time.Now().Add(-age)
	stamp := when.UnixNano()
	body := strconv.FormatInt(stamp, 10) + "\n" + rest
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return stamp
}

func writeOldStatus(t *testing.T, dir string, age time.Duration) {
	t.Helper()
	old := time.Now().Add(-age).UnixNano()
	b, err := json.Marshal(warmupStatus{Phase: "reading sessions", Total: 10, Done: 1, Started: old, Updated: old})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(warmupStatusPath(dir), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// syncBuffer collects a child's stderr while the test polls it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Under sustained I/O contention a detached `deja index` went minutes without
// publishing progress — waiting for the index lock, or stuck in a slow walk.
// Its status file went stale after 30 seconds and its claim after two
// minutes, and the next recall started a second worker beside it, then a
// third, one of which queued on the index lock (#5719). A claim now names its
// owner, and stands as long as that process runs.
func TestLiveWarmupIsNotReplaced(t *testing.T) {
	dir, spawns := ownerTestEnv(t)
	sentinel := filepath.Join(dir, "warmup.sentinel")
	// Old enough that every time-based rule, the ten-minute retry window
	// included, calls it abandoned.
	stamp := writeClaim(t, sentinel, 15*time.Minute, "")

	// The real worker, started the way requestWarmup starts it, held up on
	// the index lock another build holds.
	release := holdIndexLock(t, dir)
	released := false
	defer func() {
		if !released {
			release()
		}
	}()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "index") //nolint:noctx // waited on below; the deferred kill bounds it
	cmd.Env = append(os.Environ(), runMainEnv+"=1",
		"DEJA_WARMUP_SENTINEL="+sentinel,
		"DEJA_WARMUP_CLAIM="+strconv.FormatInt(stamp, 10))
	var stderr syncBuffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := false
	defer func() {
		if !exited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	waitFor(t, "the worker to wait for the index lock", func() bool {
		return strings.Contains(stderr.String(), "another deja is building the index")
	})

	t.Run("a worker waiting for the index lock", func(t *testing.T) {
		_ = os.Remove(warmupStatusPath(dir))
		requestWarmup(dir)
		if *spawns != 0 {
			t.Errorf("started %d warmup(s) beside a worker waiting for the lock, want 0", *spawns)
		}
		if !warmupRunning(dir) {
			t.Error("warmupRunning = false while the worker is alive")
		}
	})

	t.Run("a live worker with an old progress timestamp", func(t *testing.T) {
		writeOldStatus(t, dir, 5*time.Minute)
		requestWarmup(dir)
		if *spawns != 0 {
			t.Errorf("started %d warmup(s) beside a live worker that went quiet, want 0", *spawns)
		}
		if !warmupRunning(dir) {
			t.Error("warmupRunning = false while the worker is alive")
		}
	})

	held, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("the live worker's claim is gone: %v", err)
	}

	// The worker finishes once the lock is free and clears its own claim.
	release()
	released = true
	if err := cmd.Wait(); err != nil {
		t.Fatalf("worker: %v\n%s", err, stderr.String())
	}
	exited = true
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Errorf("the worker left its claim behind: %v", err)
	}

	// The same claim, recent this time, with its owner gone: replaced at
	// once, not after the time-based grace (#875).
	t.Run("recovery after the owner exits", func(t *testing.T) {
		_ = os.Remove(warmupStatusPath(dir))
		lines := strings.SplitN(string(held), "\n", 2)
		if len(lines) < 2 || !strings.HasPrefix(lines[1], "owner ") {
			t.Fatalf("the worker did not record itself in its claim: %q", held)
		}
		writeClaim(t, sentinel, 10*time.Second, lines[1])
		requestWarmup(dir)
		if *spawns != 1 {
			t.Errorf("started %d warmup(s) over a claim whose owner exited, want 1", *spawns)
		}
	})
}

// A claim naming this machine's pid with another start time belongs to a
// process that exited and whose pid was handed out again.
func TestWarmupClaimWithReusedPidIsReplaced(t *testing.T) {
	dir, spawns := ownerTestEnv(t)
	host, _ := os.Hostname()
	writeClaim(t, filepath.Join(dir, "warmup.sentinel"), 10*time.Second, "owner "+strconv.Itoa(os.Getpid())+" 0 "+strings.Join(strings.Fields(host), "_")+"\n")
	requestWarmup(dir)
	if *spawns != 1 {
		t.Errorf("started %d warmup(s) over a claim whose pid was reused, want 1", *spawns)
	}
}

// Another machine's claim cannot be checked from here, so its age decides,
// as before owners were recorded.
func TestWarmupClaimFromAnotherHostFallsBackToAge(t *testing.T) {
	dir, spawns := ownerTestEnv(t)
	sentinel := filepath.Join(dir, "warmup.sentinel")
	owner := "owner " + strconv.Itoa(os.Getpid()) + " 1 another-host\n"
	writeClaim(t, sentinel, 30*time.Second, owner)
	requestWarmup(dir)
	if *spawns != 0 {
		t.Errorf("started %d warmup(s) over a recent claim from another host, want 0", *spawns)
	}
	writeClaim(t, sentinel, warmupDeadAfter+time.Minute, owner)
	requestWarmup(dir)
	if *spawns != 1 {
		t.Errorf("started %d warmup(s) over an old claim from another host, want 1", *spawns)
	}
}

// A worker whose claim was replaced while it ran — by the time-based
// fallback, or by an older deja — finishes later and used to remove the
// sentinel path whatever it held, deleting the newer worker's claim (#5719).
func TestOlderWarmupDoesNotRemoveNewerClaim(t *testing.T) {
	dir, _ := ownerTestEnv(t)
	sentinel := filepath.Join(dir, "warmup.sentinel")
	older := writeClaim(t, sentinel, 5*time.Minute, "")
	newer := writeClaim(t, sentinel, time.Second, "")
	t.Setenv("DEJA_WARMUP_SENTINEL", sentinel)

	t.Setenv("DEJA_WARMUP_CLAIM", strconv.FormatInt(older, 10))
	clearWarmupSentinel()
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("the older worker removed the newer claim: %v", err)
	}

	t.Setenv("DEJA_WARMUP_CLAIM", strconv.FormatInt(newer, 10))
	clearWarmupSentinel()
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Errorf("the newer worker did not clear its own claim: %v", err)
	}
}
