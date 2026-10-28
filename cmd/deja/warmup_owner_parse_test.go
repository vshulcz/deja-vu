package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

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
