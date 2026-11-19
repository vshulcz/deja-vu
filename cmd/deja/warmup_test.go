package main

import (
	"bytes"
	"encoding/json"
	"github.com/vshulcz/deja-vu/internal/index"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// warmup threw its arguments away, so `deja warmup --rebuild` ran the ordinary
// incremental build, said nothing and exited 0; the rebuild lives on
// `deja index --rebuild` (#4109).
func TestWarmupRefusesArguments(t *testing.T) {
	withTempStores(t)
	for _, tc := range []struct {
		arg  string
		want string
	}{
		{"--rebuild", "deja index --rebuild"},
		{"-rebuild", "deja index --rebuild"},
		{"--rebuld", "deja index --rebuild"},
		{"--json", "takes no arguments"},
		{"extra", "takes no arguments"},
	} {
		_, err := captureRun(t, "warmup", tc.arg)
		if err == nil {
			t.Errorf("warmup %s was accepted", tc.arg)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), tc.arg) {
			t.Errorf("warmup %s said %v, want it to name %q and %q", tc.arg, err, tc.arg, tc.want)
		}
	}
}

// version ignored its arguments too; harmless, but it is the same trap (#4109).
func TestVersionRefusesArguments(t *testing.T) {
	for _, cmd := range []string{"version", "--version", "-version"} {
		out, err := captureRun(t, cmd)
		if err != nil || !strings.HasPrefix(out, "deja ") {
			t.Fatalf("bare %s: %q err=%v", cmd, out, err)
		}
		if _, err := captureRun(t, cmd, "--json"); err == nil || !strings.Contains(err.Error(), "takes no arguments") {
			t.Errorf("%s --json: err=%v", cmd, err)
		}
	}
}

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
	t.Setenv("DEJA_HOOK_REFRESH", "")
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

// A hook refresh can build the same index as a warmup, so it takes the same cap.
func TestAHookRefreshTakesLessThanAForegroundRun(t *testing.T) {
	if runtime.NumCPU() < 2 {
		t.Skip("one core: there is nothing to cap")
	}
	was := runtime.GOMAXPROCS(0)
	t.Cleanup(func() { runtime.GOMAXPROCS(was) })
	all := runtime.NumCPU()
	runtime.GOMAXPROCS(all)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	t.Setenv("DEJA_HOOK_REFRESH", "1")
	t.Setenv("GOMAXPROCS", "")

	takeWarmupBudget()

	if now, want := runtime.GOMAXPROCS(0), min(max(all/2, 1), 8); now != want {
		t.Errorf("the refresher kept %d of %d cores, want %d", now, all, want)
	}
}

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
	spawnWarmup = func(_, _ string) error { spawned++; return nil }
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

// warmup was the one build path that returned the raw error: a read-only
// index directory came back as `open /…/idx.lock: permission denied`, an
// internal lock file and a syscall, while `index` and `search` on the same
// directory said what to change (#798).
func TestWarmupNamesTheDirectoryWhenTheIndexCannotBeWritten(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not deny writes here")
	}
	tmp := hermeticEnv(t)
	parent := filepath.Join(tmp, "ro")
	idx := filepath.Join(parent, "idx")
	if err := os.MkdirAll(idx, 0o700); err != nil {
		t.Fatal(err)
	}
	// The build stages beside the index, so the parent is what has to deny it.
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	t.Setenv("DEJA_INDEX_DIR", idx)

	_, err := captureRun(t, "warmup")
	if err == nil {
		t.Fatal("warmup on an unwritable index directory succeeded")
	}
	got := err.Error()
	if strings.Contains(got, ".lock") || strings.Contains(got, "permission denied") {
		t.Errorf("leaks the lock file and the syscall: %q", got)
	}
	if !strings.Contains(got, "cannot write the index at "+idx) {
		t.Errorf("does not name the index directory: %q", got)
	}
}

// doctor is what someone runs when memory looks absent. On a location that
// cannot be written it answered "not built (run `deja warmup`)" — advice that
// fails the same way, so the reader learns nothing.
func TestDoctorSaysTheIndexLocationIsNotWritable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not deny writes here")
	}
	tmp := hermeticEnv(t)
	parent := filepath.Join(tmp, "ro")
	idx := filepath.Join(parent, "idx")
	if err := os.MkdirAll(idx, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	t.Setenv("DEJA_INDEX_DIR", idx)

	out, err := captureRun(t, "doctor", "--offline")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "not built (run `deja warmup`)") {
		t.Errorf("sends the reader to a command that cannot run here:\n%s", out)
	}
	if !strings.Contains(out, parent+" is not writable") {
		t.Errorf("does not name the unwritable directory:\n%s", out)
	}
}

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

// While the first index is being built there is nothing to inject, and the
// hook used to answer with silence — indistinguishable from deja being broken.
// It should say the build is running instead.
func TestSessionStartHookReportsAnInFlightBuild(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	p := newFileProgress(dir)
	p.Phase("reading sessions", 100)
	p.Advance(42)
	t.Cleanup(p.done)

	out := captureHookStdout(t, func() {
		if err := runHookContext(dir, false); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(out) == "" {
		t.Fatal("the hook stayed silent while a build was running")
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("hook emitted something that is not its envelope: %q", out)
	}
	if !strings.Contains(resp.SystemMessage, "indexing") {
		t.Errorf("systemMessage = %q, want it to explain the build", resp.SystemMessage)
	}
	// The model's context must stay clean: this is a note for the human.
	if resp.HookSpecificOutput.AdditionalContext != "" {
		t.Errorf("a progress note leaked into the model's context: %q", resp.HookSpecificOutput.AdditionalContext)
	}
}

// The plain path is injected straight into the model's context (the opencode
// plugin), where a progress line would be noise rather than a UI affordance.
func TestPlainHookStaysSilentDuringABuild(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	p := newFileProgress(dir)
	p.Phase("reading sessions", 10)
	t.Cleanup(p.done)

	out := captureHookStdout(t, func() {
		if err := runHookContext(dir, true); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("plain hook emitted %q during a build", out)
	}
}

// With no build running and nothing to recall, silence is still correct.
func TestSessionStartHookSilentWithoutABuild(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	out := captureHookStdout(t, func() {
		if err := runHookContext(dir, false); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("hook spoke with no build and no memory: %q", out)
	}
}

func captureHookStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b := make([]byte, 1<<16)
		n, _ := r.Read(b)
		done <- string(b[:n])
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

func TestParseWarmupClaim(t *testing.T) {
	cases := []struct {
		in    string
		ok    bool
		owner bool
	}{
		{"", false, false},
		{"not a stamp\n", false, false},
		{"123\n", true, false},
		{"123\nowner 42 99 host\n", true, true},
		{"123\nowner 42 99\n", true, false},
		{"123\nowner x 99 host\n", true, false},
		{"123\nowner -1 99 host\n", true, false},
		{"123\nsomething else entirely\n", true, false},
	}
	for _, c := range cases {
		got, ok := parseWarmupClaim([]byte(c.in))
		if ok != c.ok || (got.owner != nil) != c.owner {
			t.Errorf("parseWarmupClaim(%q) = %+v, %v; want ok=%v owner=%v", c.in, got, ok, c.ok, c.owner)
		}
	}
	got, _ := parseWarmupClaim([]byte("123\nowner 42 99 box\n"))
	if got.stamp != 123 || *got.owner != (warmupOwner{pid: 42, start: "99", host: "box"}) {
		t.Errorf("parsed %+v %+v", got, got.owner)
	}
	if line := got.owner.line(); line != "owner 42 99 box" {
		t.Errorf("line() = %q", line)
	}
}

func TestProcessStartOfThisProcess(t *testing.T) {
	start, alive, known := processStart(os.Getpid())
	if !known {
		t.Skip("no start time on this OS")
	}
	if !alive || start == "" {
		t.Fatalf("processStart(self) = %q, %v", start, alive)
	}
	again, _, _ := processStart(os.Getpid())
	if again != start {
		t.Errorf("start time moved: %q then %q", start, again)
	}
	if _, alive, known := processStart(0); alive || !known {
		t.Errorf("processStart(0) = alive %v known %v", alive, known)
	}
}

// A child that exited and was not waited for is a zombie on unix and a
// process with an exit code on Windows: gone either way, though its pid
// still answers.
func TestProcessStartOfAnExitedChild(t *testing.T) {
	if _, _, known := processStart(os.Getpid()); !known {
		t.Skip("no start time on this OS")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^$") //nolint:noctx // waited on below
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	deadline := time.Now().Add(30 * time.Second)
	for {
		_, alive, known := processStart(pid)
		if known && !alive {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatal("an exited, unreaped child still reads as alive")
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = cmd.Wait()
	if _, alive, known := processStart(pid); alive || !known {
		t.Errorf("reaped child: alive %v known %v", alive, known)
	}
}

func TestAdoptWarmupClaim(t *testing.T) {
	dir, _ := ownerTestEnv(t)
	sentinel := filepath.Join(dir, "warmup.sentinel")
	stamp := writeClaim(t, sentinel, time.Second, "")
	read := func() string {
		b, err := os.ReadFile(sentinel)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	want := strconv.FormatInt(stamp, 10) + "\n"

	// Not a warmup child, or a child without a claim stamp: nothing written.
	adoptWarmupClaim()
	t.Setenv("DEJA_WARMUP_SENTINEL", sentinel)
	adoptWarmupClaim()
	if got := read(); got != want {
		t.Errorf("adopted without a claim stamp: %q", got)
	}
	// Somebody else's claim is left alone.
	t.Setenv(warmupClaimEnv, "1")
	adoptWarmupClaim()
	if got := read(); got != want {
		t.Errorf("adopted another claim: %q", got)
	}
	if warmupOwnerLive(dir) {
		t.Error("a claim with no owner reads as live")
	}

	t.Setenv(warmupClaimEnv, strconv.FormatInt(stamp, 10))
	adoptWarmupClaim()
	c, ok := readWarmupClaim(dir)
	if !ok {
		t.Fatalf("claim unreadable after adoption: %q", read())
	}
	if _, _, known := processStart(os.Getpid()); !known {
		if c.owner != nil {
			t.Errorf("recorded an owner with no start time: %+v", c.owner)
		}
		return
	}
	if c.stamp != stamp || c.owner == nil || c.owner.pid != os.Getpid() || c.owner.host != warmupHost() {
		t.Fatalf("adopted claim = %q", read())
	}
	if !warmupOwnerLive(dir) || c.state() != ownerLive {
		t.Error("this process's own claim does not read as live")
	}

	// A sentinel that disappeared is not adopted back into existence.
	if err := os.Remove(sentinel); err != nil {
		t.Fatal(err)
	}
	adoptWarmupClaim()
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Errorf("adoption recreated a cleared sentinel: %v", err)
	}
	if warmupOwnerLive(dir) {
		t.Error("a missing claim reads as live")
	}
}

// A child started by an older parent, which passes no claim stamp, clears
// the path as it always did, or its sentinel would outlive every build.
func TestClearWarmupSentinelWithoutAClaimStamp(t *testing.T) {
	dir, _ := ownerTestEnv(t)
	sentinel := filepath.Join(dir, "warmup.sentinel")
	writeClaim(t, sentinel, time.Second, "")
	clearWarmupSentinel()
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("cleared with no sentinel variable set: %v", err)
	}
	t.Setenv("DEJA_WARMUP_SENTINEL", sentinel)
	clearWarmupSentinel()
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Errorf("sentinel left behind: %v", err)
	}
}

// The parent names the child in the claim as soon as it is spawned, so a
// child that stalls before running a line of its own still reads as alive.
func TestStartDetachedWarmupNamesItsChild(t *testing.T) {
	if _, _, known := processStart(os.Getpid()); !known {
		t.Skip("no start time on this OS")
	}
	dir, _ := ownerTestEnv(t)
	sentinel := filepath.Join(dir, "warmup.sentinel")
	stamp := writeClaim(t, sentinel, time.Second, "")
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
	t.Setenv(runMainEnv, "1")
	if err = startDetachedWarmup(exe, sentinel); err != nil {
		t.Fatal(err)
	}
	t.Setenv(runMainEnv, "")
	c, ok := readWarmupClaim(dir)
	if !ok || c.stamp != stamp || c.owner == nil || c.owner.pid == os.Getpid() {
		t.Fatalf("claim after spawn = %+v %v", c, ok)
	}
	if c.state() != ownerLive {
		t.Error("the spawned child does not read as live")
	}
	release()
	released = true
	waitFor(t, "the child to clear its claim", func() bool {
		_, err := os.Stat(sentinel)
		return os.IsNotExist(err)
	})
}

// A claim replaced before the parent got to it is not written over.
func TestRecordWarmupOwnerLeavesAnotherClaim(t *testing.T) {
	dir, _ := ownerTestEnv(t)
	sentinel := filepath.Join(dir, "warmup.sentinel")
	recordWarmupOwner(sentinel, 1, os.Getpid())
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("recreated a cleared claim: %v", err)
	}
	stamp := writeClaim(t, sentinel, time.Second, "")
	recordWarmupOwner(sentinel, stamp+1, os.Getpid())
	if c, _ := readWarmupClaim(dir); c.owner != nil {
		t.Errorf("wrote an owner into another claim: %+v", c.owner)
	}
}

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

// Everything the user sees about a cold build — the status bar line, the
// hook's systemMessage, the opencode toast, the MCP instructions note — reads
// one status file. If the detached build stops writing it, all of them go
// quiet at once and nothing else fails, so this pins the whole path rather
// than fileProgress in isolation.
func TestWarmupPublishesStatusDuringBuild(t *testing.T) {
	withTempStores(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	if dir == "" {
		t.Fatal("DEJA_INDEX_DIR not set by withTempStores")
	}
	t.Setenv("DEJA_WARMUP_SENTINEL", dir+"/warmup.sentinel")
	seen := false
	err := withWarmupStatus(dir, func() error {
		if err := index.Ensure(dir, "", true, os.Stderr); err != nil {
			return err
		}
		_, statErr := os.Stat(warmupStatusPath(dir))
		seen = statErr == nil
		return nil
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !seen {
		t.Fatal("no status file during the build: the status bar and hooks have nothing to report")
	}
	// And it must not outlive the build, or the agent claims forever that
	// memory is on its way.
	if _, err := os.Stat(warmupStatusPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("status file survived the build: %v", err)
	}
}

// A status file exists before its phase is written, and every agent-facing
// sentence interpolates progress() into parentheses — so that window read
// "indexing this machine's history ()". line() has always substituted a word
// there; progress() did not (#1731).
func TestBuildingSentenceNeverShowsAnEmptyPhase(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	write := func(st warmupStatus) {
		st.Updated = time.Now().UnixNano()
		b, err := json.Marshal(st)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(warmupStatusPath(dir), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for _, st := range []warmupStatus{{}, {Done: 3, Total: 10}} {
		write(st)
		got := buildingNowForAgent(dir)
		if got == "" {
			t.Fatalf("no sentence at all for %+v", st)
		}
		if strings.Contains(got, "()") || strings.Contains(got, "( ") {
			t.Errorf("%+v renders an empty phase: %q", st, got)
		}
	}

	// The phase, when there is one, still reads as before.
	write(warmupStatus{Phase: "finding transcripts", Done: 1, Total: 4})
	if got := buildingNowForAgent(dir); !strings.Contains(got, "finding transcripts 25%") {
		t.Errorf("a real phase went missing: %q", got)
	}
}

// The warmup child is spawned with stdout on /dev/null, a character device
// that passes for a terminal — so the live display took over the sink that
// publishes progress and every "memory is on its way" line had nothing to
// read (#862).
func TestWarmupPublishesStatusWhenStdoutLooksLikeATerminal(t *testing.T) {
	tmp := hermeticEnv(t)
	chats := filepath.Join(tmp, "qwen", "projects", "proj", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"q-1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","parts":[{"text":"pool exhausted"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(chats, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	t.Setenv("DEJA_WARMUP_SENTINEL", filepath.Join(tmp, "sentinel"))

	saved := logoWanted
	logoWanted = func(*os.File) bool { return true }
	t.Cleanup(func() { logoWanted = saved })

	dir := filepath.Join(tmp, "idx")
	var published bool
	err := withWarmupStatus(dir, func() error {
		return withBuildProgress(func() error {
			if err := index.Ensure(dir, "", false, nil); err != nil {
				return err
			}
			// Still inside withWarmupStatus, so the file has not been
			// cleaned up yet: what a reader would have seen mid-build.
			_, err := os.Stat(warmupStatusPath(dir))
			published = err == nil
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !published {
		t.Error("no warmup status was published while the index was building")
	}
	// And it is cleaned up: a status left behind tells every later session
	// that memory is still coming when the build finished long ago.
	if _, err := os.Stat(warmupStatusPath(dir)); !os.IsNotExist(err) {
		t.Errorf("warmup status survived the build: %v", err)
	}
}

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

// "Written whole and renamed: a reader must never see half a record" is the
// invariant on the status file. It held for one writer and not for two — and
// two is ordinary, since two agents starting together each spawn a detached
// build. Both derived the temp name from the destination, so one truncated what
// the other was renaming: 180 of 18049 reads came back unparseable (#1319).
func TestTwoBuildsNeverPublishHalfAStatus(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := warmupStatusPath(dir)

	var stop atomic.Bool
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			f := &fileProgress{path: p}
			for i := 0; i < 400 && !stop.Load(); i++ {
				f.st = warmupStatus{Phase: "claude", Done: i * (w + 1), Total: i * 100, Stores: 7}
				f.flush(true)
			}
		}(w)
	}
	var reads, empty, bad int64
	wg.Go(func() {
		for !stop.Load() {
			raw, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			atomic.AddInt64(&reads, 1)
			if len(raw) == 0 {
				atomic.AddInt64(&empty, 1)
				continue
			}
			var got warmupStatus
			if json.Unmarshal(raw, &got) != nil {
				atomic.AddInt64(&bad, 1)
			}
		}
	})
	// Run until the reader has actually read something, not for a fixed slice
	// of time: on a loaded runner 300 ms passed with the reader never scheduled,
	// and the test failed saying it proved nothing — which was true, and about
	// the runner rather than the writers.
	deadline := time.Now().Add(10 * time.Second)
	for atomic.LoadInt64(&reads) < 50 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	stop.Store(true)
	wg.Wait()
	if reads == 0 {
		t.Fatal("the reader never saw the file, so this proves nothing")
	}
	// Every unparseable read tells a surface no build is running while one is:
	// readWarmupStatus returns nil on a record it cannot decode, and the agent
	// is handed silence in the one state the sentence exists for.
	if bad > 0 || empty > 0 {
		t.Errorf("%d of %d reads saw a torn status (%d empty) — two writers shared one temp name", bad+empty, reads, empty)
	}
}

// The build reports from several goroutines at once — harness stores are
// parsed concurrently — so publishing must not race for its temporary file.
func TestWarmupStatusSurvivesConcurrentReports(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	p := newFileProgress(dir)
	p.Phase("reading sessions", 100)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 40 {
				p.Advance(1)
				p.Harness("claude", 1, 1)
			}
		})
	}
	wg.Wait()
	st := readWarmupStatus(dir)
	if st == nil {
		t.Fatal("no status was published")
	}
	if st.Phase != "reading sessions" {
		t.Errorf("phase = %q", st.Phase)
	}
	p.done()
	if readWarmupStatus(dir) != nil {
		t.Error("the status outlived the build")
	}
}

// It lives beside the index, never inside: a first build has not created the
// index directory yet, and the atomic swap that publishes a rebuild replaces
// everything within it.
func TestWarmupStatusLivesOutsideTheIndex(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if strings.HasPrefix(warmupStatusPath(dir), dir+string(os.PathSeparator)) {
		t.Fatalf("status path %q is inside the index directory", warmupStatusPath(dir))
	}
	p := newFileProgress(dir)
	p.Phase("reading sessions", 3)
	if readWarmupStatus(dir) == nil {
		t.Fatal("status could not be written before the index directory exists")
	}
}

// A warmup killed mid-build must not leave agents claiming forever that
// memory is on its way.
func TestWarmupStatusExpires(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	p := newFileProgress(dir)
	p.Phase("indexing messages", 10)
	if readWarmupStatus(dir) == nil {
		t.Fatal("fresh status was not read back")
	}
	old := time.Now().Add(-2 * warmupStatusStale)
	if err := os.Chtimes(warmupStatusPath(dir), old, old); err != nil {
		t.Fatal(err)
	}
	// Staleness is judged by the stamp inside the record, not the mtime, so
	// rewrite the record with an old stamp.
	b, err := os.ReadFile(warmupStatusPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	stale := strings.Replace(string(b), `"updated":`, `"updated_ignored":`, 1)
	if err := os.WriteFile(warmupStatusPath(dir), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	if readWarmupStatus(dir) != nil {
		t.Error("a record with no timestamp was treated as a live build")
	}
}

func TestWarmupStatusLineReadsLikeASentence(t *testing.T) {
	st := &warmupStatus{Phase: "reading sessions", Done: 30, Total: 60}
	line := st.line()
	for _, want := range []string{"deja", "reading sessions", "50%"} {
		if !strings.Contains(line, want) {
			t.Errorf("status line %q is missing %q", line, want)
		}
	}
	// An unknown total must not print a nonsense percentage.
	if strings.Contains((&warmupStatus{Phase: "starting"}).line(), "%") {
		t.Error("a phase with no total claimed a percentage")
	}
}

// warmup-status prints one line while a build runs, nothing otherwise: hosts
// branch on emptiness, so a stray newline would read as "still building".
func TestWarmupStatusCommandIsSilentWithoutABuild(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	out := captureHookStdout(t, func() {
		if err := cmdWarmupStatus(dir, nil); err != nil {
			t.Fatal(err)
		}
	})
	if out != "" {
		t.Errorf("printed %q with no build running", out)
	}
	p := newFileProgress(dir)
	p.Phase("reading sessions", 8)
	p.Advance(2)
	t.Cleanup(p.done)
	out = captureHookStdout(t, func() {
		if err := cmdWarmupStatus(dir, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "indexing") {
		t.Errorf("printed %q during a build", out)
	}
}

// A harness with no auto-recall wiring reaches deja only over MCP. A recall
// during the first build must not answer with a confident negative, or the
// agent tells the user they have no history at all.
func TestMCPRecallDistinguishesEmptyFromStillBuilding(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")

	quiet := emptyRecallAnswer(dir, "jwt skew")
	if !strings.Contains(quiet, "No prior deja sessions matched") {
		t.Errorf("with no build running the answer should be a plain negative, got %q", quiet)
	}

	p := newFileProgress(dir)
	p.Phase("reading sessions", 10)
	p.Advance(3)
	t.Cleanup(p.done)

	building := emptyRecallAnswer(dir, "jwt skew")
	if strings.Contains(building, "No prior deja sessions matched") {
		t.Fatalf("a recall during the build still reads as a negative: %q", building)
	}
	// Not asserting the percentage here: publishing is throttled to 250ms, so
	// a freshly reported Advance may not have reached the file yet. The
	// percentage itself is covered by TestWarmupProgressFragment.
	for _, want := range []string{"building", "reading sessions", "ask again"} {
		if !strings.Contains(building, want) {
			t.Errorf("answer %q is missing %q", building, want)
		}
	}
}

// A refresh over an index that already answers is not a first build: the
// recall searched every session indexed so far, and "nothing can be recalled
// yet" denied history it had just searched.
func TestMCPRecallDuringARefreshIsAPlainMiss(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	p := newFileProgress(dir)
	p.Phase("reading sessions", 10)
	t.Cleanup(p.done)
	got := emptyRecallAnswer(dir, "zzqqnomatchword")
	if strings.Contains(got, "still building") || !strings.Contains(got, "No prior deja sessions matched") || !strings.Contains(got, "refreshing") {
		t.Errorf("a miss during a refresh answered %q", got)
	}
}

func TestWarmupProgressFragment(t *testing.T) {
	if got := (&warmupStatus{Phase: "indexing messages", Done: 1, Total: 4}).progress(); got != "indexing messages 25%" {
		t.Errorf("progress = %q", got)
	}
	// No total means no invented percentage.
	if got := (&warmupStatus{Phase: "starting"}).progress(); got != "starting" {
		t.Errorf("progress with unknown total = %q", got)
	}
}

// Claude Code gets a relevance pass on every prompt; opencode has the same
// opening in the session context hook, and without it recall there is only
// ever as good as the session digest.
func TestOpencodePluginRecallsPerPrompt(t *testing.T) {
	src := opencodePluginJS("/bin/deja")
	for _, want := range []string{
		`ctx.session.hook("context"`,
		`role !== "user"`, // user turns, not every message
		"hook-prompt",
		"additionalContext",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("generated plugin missing %q:\n%s", want, src)
		}
	}
	// It adds to the prompt rather than replacing it.
	if !strings.Contains(src, `body + "\n\n" + keep`) {
		t.Fatalf("recall does not append to the user's own text:\n%s", src)
	}
}

// opencode spawns agents through its `subagent` tool — 817 of them on the
// store this was measured against — and none of the plugin's other hooks
// reach one: the system prompt is built for the session that spawned it,
// and the per-prompt pass fires on what a person typed. Its instructions are
// the only thing that arrives, so recall has to be put there.
func TestOpencodePluginCarriesRecallIntoASpawnedAgent(t *testing.T) {
	src := opencodePluginJS("/bin/deja")
	for _, want := range []string{
		`ctx.tool.hook("execute.before"`,
		`event.tool !== "subagent"`,
		"hook-tool",
		"updatedInput",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("generated plugin missing %q:\n%s", want, src)
		}
	}
	// The spawn is rewritten, not blocked, and a spawn deja has nothing to say
	// about keeps the prompt its parent wrote.
	if !strings.Contains(src, "if (next) event.input = { ...args, prompt: next }") {
		t.Fatalf("plugin does not leave a silent recall alone:\n%s", src)
	}
}

// Compaction throws away the working transcript. Claude Code gets it indexed
// first through PreCompact; opencode fires its compaction hook at the same
// moment and was going unused.
func TestOpencodePluginIndexesBeforeCompaction(t *testing.T) {
	src := opencodePluginJS("/bin/deja")
	if !strings.Contains(src, `ctx.session.hook("compaction"`) {
		t.Fatalf("compaction passes without indexing:\n%s", src)
	}
	if !strings.Contains(src, "hook-precompact") {
		t.Fatalf("compaction hook does not index:\n%s", src)
	}
}

// TestWarmupProgressNamesAnUnwrittenPhase covers #1731: a status file that
// exists but has not had its phase written yet left progress() returning "",
// and every caller wraps that fragment in a parenthetical — so the sentence
// handed to an agent was "deja is indexing this machine's history ()."
func TestWarmupProgressNamesAnUnwrittenPhase(t *testing.T) {
	tests := []struct {
		name string
		st   warmupStatus
		want string
	}{
		{name: "phase not yet written, no total", st: warmupStatus{}, want: "starting"},
		{name: "phase not yet written, with total", st: warmupStatus{Done: 1, Total: 4}, want: "starting 25%"},
		{name: "phase written, no total", st: warmupStatus{Phase: "finding transcripts…"}, want: "finding transcripts…"},
		{name: "phase written, with total", st: warmupStatus{Phase: "indexing messages", Done: 1, Total: 4}, want: "indexing messages 25%"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.st.progress(); got != tt.want {
				t.Errorf("progress() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestWarmupSentencesNeverShowAnEmptyParenthetical is the property the fix
// exists to protect, asserted on the rendered sentences rather than on the
// fragment: no surface may hand a user or an agent "()".
func TestWarmupSentencesNeverShowAnEmptyParenthetical(t *testing.T) {
	st := &warmupStatus{}
	for _, got := range []string{st.progress(), st.line()} {
		if got == "" || strings.Contains(got, "()") {
			t.Errorf("rendered %q with an unwritten phase, want a named phase", got)
		}
	}
}

// doctor and the hook both say when the index is being rebuilt; the statusline
// said "no recalls yet today" and the MCP instructions said nothing at all —
// a normal day, while the index on disk is one this build cannot read (#879).
func TestEverySurfaceSaysWhenTheIndexIsBeingRebuilt(t *testing.T) {
	hermeticEnv(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	statusline := func() string {
		var out bytes.Buffer
		if err := runStatusline(dir, strings.NewReader(`{"session_id":"x","cwd":"/proj"}`), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	// Healthy: neither surface mentions a build.
	if got := statusline(); strings.Contains(got, "rebuilding") {
		t.Errorf("a healthy index was called a rebuild: %q", got)
	}
	if got := mcpInstructions(dir, ""); strings.Contains(got, "being rebuilt") {
		t.Errorf("a healthy index was called a rebuild: %q", got)
	}

	// A build asked for moments ago that has not published progress yet.
	if err := os.WriteFile(filepath.Join(dir, "warmup.sentinel"),
		[]byte(strconv.FormatInt(time.Now().UnixNano(), 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(warmupStatusPath(dir))

	if got := statusline(); !strings.Contains(got, "rebuilding the index") {
		t.Errorf("statusline claims an ordinary day during a rebuild: %q", got)
	}
	if got := mcpInstructions(dir, ""); !strings.Contains(got, "being rebuilt right now") {
		t.Errorf("MCP instructions say nothing about the rebuild: %q", got)
	}

	// A machine with no index is not told its index is being rebuilt.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "warmup.sentinel"),
		[]byte(strconv.FormatInt(time.Now().UnixNano(), 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := statusline(); strings.Contains(got, "rebuilding") {
		t.Errorf("a machine with no index was told about a rebuild: %q", got)
	}
	if got := mcpInstructions(dir, ""); strings.Contains(got, "being rebuilt") {
		t.Errorf("a machine with no index was told about a rebuild: %q", got)
	}
}
