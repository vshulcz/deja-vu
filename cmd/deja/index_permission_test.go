package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The command whose whole job is building the index used to pass the syscall
// through: `mkdir /…/index.db.tmp: permission denied` names an internal temp
// path and no fix, while every reading command has said what to change since
// ensureError was written (#798).
func TestIndexExplainsADeniedWrite(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	hermeticEnv(t)
	home := os.Getenv("HOME")
	store := filepath.Join(home, ".claude", "projects", "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"gypsy wildcat pitch mismatch"},"timestamp":"2026-07-04T10:00:00Z","sessionId":"p4d","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.MkdirAll(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	err := cmdIndex(filepath.Join(locked, "index.db"), nil)
	if err == nil {
		t.Fatal("a denied write reported success")
	}
	if !strings.Contains(err.Error(), "check the directory's permissions") {
		t.Errorf("error does not say what to change: %v", err)
	}
	if strings.Contains(err.Error(), ".tmp") || strings.Contains(err.Error(), "mkdir") {
		t.Errorf("error still leaks the syscall and an internal path: %v", err)
	}
}
