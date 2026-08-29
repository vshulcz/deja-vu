package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// After an upgrade the session start repairs the wiring it recorded and says
// what it rewrote. When one target refuses — a config someone hand-broke, a
// read-only path — the record is deliberately left unstamped (#2212), so every
// later start tries again and prints the same line. Nothing says which target
// is stuck, or that anything failed at all (#2594).
func TestTheSessionStartNamesAWiringItCouldNotRepair(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if _, err := captureRun(t, "install", "claude-code", "--no-index", "--no-guidance"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "cursor", "--no-index", "--no-guidance"); err != nil {
		t.Fatal(err)
	}
	// One of them is now unreadable as config: deja will not edit it.
	broken := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(broken, []byte("{ this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The record says an older build wrote the wiring, so the repair runs.
	path := filepath.Join(home, ".config", "deja", "wiring.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	aged := strings.Replace(string(b), `"version": "dev"`, `"version": "0.0.1"`, 1)
	if aged == string(b) {
		t.Fatalf("the fixture record has no version to age:\n%s", b)
	}
	if err := os.WriteFile(path, []byte(aged), 0o600); err != nil {
		t.Fatal(err)
	}

	said, err := captureRun(t, "hook-context")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(said, "claude") {
		t.Errorf("the start said nothing about the wiring it could not repair:\n%s", said)
	}
	// And the record still says the old version, so the next start tries again
	// — which is right, and is exactly why the reader has to be told.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "0.0.1") {
		t.Errorf("the record was stamped despite the failure:\n%s", after)
	}
}
