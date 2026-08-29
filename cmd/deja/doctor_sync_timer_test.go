package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writeSyncPlist puts the file install writes there by hand. Nothing is loaded
// into launchd: the suite must never register a live agent (see
// runServiceManager's guard).
func writeSyncPlist(t *testing.T, exe string) {
	t.Helper()
	path := syncAutoPlistPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(syncAutoPlist(exe)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSyncUnit(t *testing.T, exe string) {
	t.Helper()
	dir := syncAutoUnitDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deja-sync.service"), []byte(syncAutoService(exe)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deja-sync.timer"), []byte(syncAutoTimer()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSyncTimerFor(t *testing.T, exe string) {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		writeSyncPlist(t, exe)
	case "linux":
		writeSyncUnit(t, exe)
	default:
		t.Skip("no timer on this platform")
	}
}

func doctorSyncSection(t *testing.T, dir string) string {
	t.Helper()
	var b bytes.Buffer
	doctorPeers(&b, dir, time.Now())
	return b.String()
}

// The Sync section exists because a sync that stops does not announce itself.
// The thing that runs the sync was not on it: `install --all` writes a timer
// and nothing afterwards reported on it (#2636).
func TestDoctorSaysTheSyncTimerIsNotScheduled(t *testing.T) {
	if !syncTimerSchedulable(runtime.GOOS) {
		t.Skip("no timer on this platform")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	out := doctorSyncSection(t, t.TempDir())
	if !strings.Contains(out, "timer") {
		t.Fatalf("the Sync section never mentions the timer:\n%s", out)
	}
	if !strings.Contains(out, "deja install sync-timer") {
		t.Fatalf("nothing says how to schedule it:\n%s", out)
	}
}

// Scheduled, and pointing at a binary that is gone: deja moved or was upgraded,
// the file kept the old path, and the hourly sync stopped.
func TestDoctorSaysTheSyncTimerPointsAtAMissingBinary(t *testing.T) {
	if !syncTimerSchedulable(runtime.GOOS) {
		t.Skip("no timer on this platform")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	writeSyncTimerFor(t, filepath.Join(home, "gone", "deja"))
	out := doctorSyncSection(t, t.TempDir())
	if !strings.Contains(out, "no longer there") {
		t.Fatalf("a timer pointing at a missing binary reads as healthy:\n%s", out)
	}
}

// Scheduled and pointing at a binary that exists: say so plainly and stop.
func TestDoctorSaysTheSyncTimerIsScheduled(t *testing.T) {
	if !syncTimerSchedulable(runtime.GOOS) {
		t.Skip("no timer on this platform")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	exe := filepath.Join(home, "deja")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSyncTimerFor(t, exe)
	out := doctorSyncSection(t, t.TempDir())
	if !strings.Contains(out, "every 30 min") {
		t.Fatalf("the schedule is not reported:\n%s", out)
	}
	if strings.Contains(out, "no longer there") {
		t.Fatalf("a healthy timer was called broken:\n%s", out)
	}
}
