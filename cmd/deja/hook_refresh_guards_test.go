package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The spawn is refused under `go test` on purpose, which is what makes the
// guards observable here at all: what a test can see is the sentinel, not a
// process. If that ever stops holding these tests would leave refreshers
// behind, so they check it rather than assume it.
func requireNoSpawnHere(t *testing.T) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("cannot tell whether a refresher would be spawned: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSuffix(exe, ".exe"), ".test") {
		t.Fatalf("this binary would spawn a real refresher: %s", exe)
	}
}

// requestHookRefresh spawns a detached refresher, so its guards decide how
// often a machine does that: once per two minutes per directory, and never
// from inside a refresh. Nothing tested any of them — removing the recursion
// guard, the stampede window, its expiry, or the sentinel write all passed the
// package.
func TestHookRefreshWritesTheSentinelOnce(t *testing.T) {
	requireNoSpawnHere(t)
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "index.db")
	cwd := filepath.Join(tmp, "work")
	sentinel := hookCachePath(dir, cwd) + ".refreshing"

	requestHookRefresh(dir, cwd)
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("no sentinel after the first request: %v", err)
	}

	// Mark the file, then ask again inside the window. A second write would
	// replace the mark — which says the guard held without depending on how
	// coarse the filesystem's timestamps are.
	const mark = "left by the test\n"
	if err := os.WriteFile(sentinel, []byte(mark), 0o600); err != nil {
		t.Fatal(err)
	}
	requestHookRefresh(dir, cwd)

	got, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("the sentinel vanished: %v", err)
	}
	if string(got) != mark {
		t.Errorf("a second request inside the window rewrote the sentinel: %q", string(got))
	}
}

// Past the window the next request takes over: the refresher it stood for is
// either long done or never started, and nothing would refresh again while the
// file sat there.
func TestHookRefreshRetriesPastTheWindow(t *testing.T) {
	requireNoSpawnHere(t)
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "index.db")
	cwd := filepath.Join(tmp, "work")
	sentinel := hookCachePath(dir, cwd) + ".refreshing"

	const mark = "left by the test\n"
	if err := os.WriteFile(sentinel, []byte(mark), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-3 * time.Minute)
	if err := os.Chtimes(sentinel, stale, stale); err != nil {
		t.Fatal(err)
	}

	requestHookRefresh(dir, cwd)

	got, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("the sentinel vanished: %v", err)
	}
	if string(got) == mark {
		t.Errorf("a stale sentinel blocked the next refresh: still %q", string(got))
	}
}

// A refresher waiting for the index lock can outlive the window (#5719).
func TestHookRefreshClaimOwner(t *testing.T) {
	requireNoSpawnHere(t)
	start, alive, known := processStart(os.Getpid())
	if !known || !alive {
		t.Skip("this platform cannot name a process: a claim falls back to its age")
	}
	for _, tt := range []struct {
		name  string
		age   time.Duration
		start string
		held  bool
	}{
		{"live past the window", 3 * time.Minute, start, true},
		{"gone past the window", 3 * time.Minute, "0", false},
		{"gone inside the window", 10 * time.Second, "0", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tmp := hermeticEnv(t)
			t.Setenv("DEJA_HOOK_REFRESH", "")
			dir := filepath.Join(tmp, "index.db")
			cwd := filepath.Join(tmp, "work")
			sentinel := hookCachePath(dir, cwd) + ".refreshing"
			owner := warmupOwner{pid: os.Getpid(), start: tt.start, host: warmupHost()}
			writeClaim(t, sentinel, tt.age, owner.line()+"\n")
			before, err := os.ReadFile(sentinel)
			if err != nil {
				t.Fatal(err)
			}

			requestHookRefresh(dir, cwd)

			got, err := os.ReadFile(sentinel)
			if err != nil {
				t.Fatalf("the sentinel vanished: %v", err)
			}
			if tt.held {
				if !bytes.Equal(got, before) {
					t.Errorf("a held claim was replaced: %q", got)
				}
			} else {
				claim, ok := parseWarmupClaim(got)
				if !ok || claim.stamp <= 1 || bytes.Equal(got, before) {
					t.Errorf("an expired claim was not replaced with a request stamp: %q", got)
				}
			}
		})
	}
}

// The claim has to name the process that was started. Without that a waiting
// refresher reads as having no owner, and the next request replaces it once the
// window passes.
func TestHookRefreshNamesWhatItStarted(t *testing.T) {
	if _, alive, known := processStart(os.Getpid()); !known || !alive {
		t.Skip("this platform cannot name a process: a claim falls back to its age")
	}
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_HOOK_REFRESH", "")
	dir := filepath.Join(tmp, "index.db")
	cwd := filepath.Join(tmp, "work")
	var env []string
	saved := startRefresher
	startRefresher = func(cmd *exec.Cmd) (int, error) {
		env = cmd.Env
		return os.Getpid(), nil
	}
	t.Cleanup(func() { startRefresher = saved })

	requestHookRefresh(dir, cwd)

	b, err := os.ReadFile(hookCachePath(dir, cwd) + ".refreshing")
	if err != nil {
		t.Fatal(err)
	}
	if claim, ok := parseWarmupClaim(b); !ok || claim.state() != ownerLive {
		t.Errorf("the sentinel does not name the started process: %q", b)
	}
	if !slices.Contains(env, "DEJA_HOOK_REFRESH=1") || !slices.Contains(env, "CLAUDE_PROJECT_DIR="+cwd) {
		t.Errorf("the refresher was not told what it is or where: %q", env)
	}
}

// A refresher must not ask for a refresh. The variable it runs under is the
// only thing stopping that, and it is checked before the sentinel is touched,
// so a refresh in progress leaves no trace of a request it did not make.
func TestHookRefreshDoesNotRecurse(t *testing.T) {
	requireNoSpawnHere(t)
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "index.db")
	cwd := filepath.Join(tmp, "work")
	sentinel := hookCachePath(dir, cwd) + ".refreshing"

	// A positive control first: the same call in the same place does write
	// that file, so the absence below is the guard and not a path this test
	// computed differently from the code.
	requestHookRefresh(dir, cwd)
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("wrong path or no sentinel to begin with: %v", err)
	}
	if err := os.Remove(sentinel); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DEJA_HOOK_REFRESH", "1")
	requestHookRefresh(dir, cwd)

	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Errorf("a refresher asked for a refresh: %v", err)
	}
}
