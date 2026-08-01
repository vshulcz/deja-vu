package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Moving a binary without changing its version is ordinary — a relink, a
// reinstall of the same release, a `go install` over a manual download — and
// left every config naming a path that no longer exists (#773).
func TestWiringRepairFollowsAMovedBinary(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-code", "--no-index"); err != nil {
		t.Fatal(err)
	}
	wired := readWiringState()
	if wired.Exe == "" {
		t.Fatal("install recorded no binary path")
	}
	if len(wired.Targets) == 0 {
		t.Fatal("install recorded no targets")
	}

	// Pretend the binary was somewhere else when the configs were written.
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
	if err := os.WriteFile(claudeJSON, []byte(strings.ReplaceAll(string(before), moved, wired.Exe)), 0o644); err != nil {
		t.Fatal(err)
	}

	if changed := refreshWiringAfterUpgrade(); len(changed) == 0 {
		t.Error("a moved binary did not trigger the repair")
	}
	after, err := os.ReadFile(claudeJSON)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "old-location") {
		t.Errorf("config still names the old path:\n%s", after)
	}

	// Nothing moved and nothing upgraded: the repair must stay quiet, or every
	// session start rewrites the user's configs.
	if changed := refreshWiringAfterUpgrade(); len(changed) != 0 {
		t.Errorf("repair ran with nothing to repair: %v", changed)
	}
}
