//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// holdTheInstallLock takes the install lock the way another deja process would.
// flock is held per open file, so a second handle in this process conflicts
// with it exactly as another process's would — the same arrangement
// holdTheIndexLock uses, and its own file for the same reason.
func holdTheInstallLock(t *testing.T) {
	t.Helper()
	path := installLockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		t.Fatalf("could not hold the install lock: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	})
}

// Two processes writing one config from the state each read first loses one of
// the two wirings — measured at eight runs in twelve with `deja install
// claude-auto` and `deja install statusline` started together. The repair is
// the half that fires unasked, from a session start, so it stands down rather
// than waiting: the process holding the lock is writing the same wiring
// (#3691).
func TestTheRepairStandsDownWhileAnInstallHoldsTheLock(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-code", "--no-index"); err != nil {
		t.Fatal(err)
	}
	wired := readWiringState()
	moved := wired.Exe
	wired.Exe = filepath.Join(home, "old-location", "deja")
	b, err := json.Marshal(wired)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wiringStatePath(), b, 0o644); err != nil {
		t.Fatal(err)
	}
	claudeJSON := filepath.Join(home, ".claude.json")
	before, err := os.ReadFile(claudeJSON)
	if err != nil {
		t.Fatal(err)
	}
	stale := strings.ReplaceAll(string(before), moved, wired.Exe)
	if err := os.WriteFile(claudeJSON, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	holdTheInstallLock(t)
	if changed := refreshWiringAfterUpgrade(); len(changed) != 0 {
		t.Errorf("the repair ran while another process held the lock: %v", changed)
	}
	after, err := os.ReadFile(claudeJSON)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != stale {
		t.Errorf("the config was rewritten under another process's lock:\n%s", after)
	}
}

// And the primitive says which of the two failures it hit, because only one of
// them is a reason to skip work.
func TestTheInstallLockSaysWhenSomebodyElseHasIt(t *testing.T) {
	hermeticEnv(t)
	holdTheInstallLock(t)
	release, ok, busy := holdInstallLock(false)
	defer release()
	if ok {
		t.Error("the lock was handed out twice")
	}
	if !busy {
		t.Error("a lock held elsewhere was not reported as busy")
	}
}
