package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMCPOrphanHelperProcess(t *testing.T) {
	switch os.Getenv("DEJA_MCP_ORPHAN_HELPER") {
	case "":
		return
	case "wait":
		time.Sleep(time.Hour)
	}
	os.Exit(0)
}

func mcpOrphanHelper(mode string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestMCPOrphanHelperProcess$")
	cmd.Env = append(os.Environ(), "DEJA_MCP_ORPHAN_HELPER="+mode)
	return cmd
}

// deadMCPProcessPID returns a pid that no longer runs while the *exec.Cmd
// still holds its process handle — the exact state where OpenProcess keeps
// succeeding, so a FindProcess-only processAlive fails here by construction.
func deadMCPProcessPID(t *testing.T) int {
	t.Helper()
	cmd := mcpOrphanHelper("exit")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run helper process: %v", err)
	}
	t.Cleanup(func() { runtime.KeepAlive(cmd) })
	return cmd.Process.Pid
}

func liveMCPProcess(t *testing.T) (*exec.Cmd, func()) {
	t.Helper()
	cmd := mcpOrphanHelper("wait")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	return cmd, func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
}

func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("current process reported dead")
	}

	pid := deadMCPProcessPID(t)
	if processAlive(pid) {
		t.Fatalf("waited process %d reported alive", pid)
	}
}

func TestOrphanWatchKeepsLiveParentDespiteSilence(t *testing.T) {
	cmd, stopParent := liveMCPProcess(t)
	defer stopParent()

	exited := make(chan struct{}, 1)
	stop := startOrphanWatch(
		cmd.Process.Pid,
		func() time.Time { return time.Now().Add(-time.Hour) },
		10*time.Millisecond,
		5*time.Millisecond,
		func() { exited <- struct{}{} },
	)
	defer stop()

	select {
	case <-exited:
		t.Fatal("watch exited while the parent was alive")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestOrphanWatchKeepsRecentlyActiveDeadParent(t *testing.T) {
	pid := deadMCPProcessPID(t)
	lastRead := time.Now()
	exited := make(chan struct{}, 1)
	stop := startOrphanWatch(
		pid,
		func() time.Time { return lastRead },
		2*time.Second,
		5*time.Millisecond,
		func() { exited <- struct{}{} },
	)
	defer stop()

	select {
	case <-exited:
		t.Fatal("watch exited before the read grace period elapsed")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestOrphanWatchExitsForDeadSilentParent(t *testing.T) {
	pid := deadMCPProcessPID(t)
	exited := make(chan struct{}, 1)
	stop := startOrphanWatch(
		pid,
		func() time.Time { return time.Now().Add(-time.Hour) },
		10*time.Millisecond,
		5*time.Millisecond,
		func() { exited <- struct{}{} },
	)
	defer stop()

	select {
	case <-exited:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("watch did not exit for a dead parent after prolonged silence")
	}
}

func TestMCPStampReaderUpdatesLastRead(t *testing.T) {
	var lastRead atomic.Int64
	lastRead.Store(time.Now().Add(-time.Hour).UnixNano())
	before := time.Now().UnixNano()

	r := &mcpStampReader{
		r:        strings.NewReader("x"),
		lastRead: &lastRead,
	}
	buf := make([]byte, 1)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if n != 1 || string(buf) != "x" {
		t.Fatalf("read = %d, %q; want 1, %q", n, buf, "x")
	}
	if got := lastRead.Load(); got < before {
		t.Fatalf("last read = %d; want at least %d", got, before)
	}
}
