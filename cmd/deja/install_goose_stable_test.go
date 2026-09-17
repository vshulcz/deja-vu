package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Goose keeps the slash command and the MCP extension in one config.yaml, and
// each writer removed its block and added it back — which, when the block held
// only deja's entry, took the key with it and re-appended it at the bottom.
// The two keys swapped places on every install, the report said "updated"
// twice with nothing to update, and a config.yaml in a dotfiles repository
// showed a diff after each upgrade's repair (#3689).
func TestInstallGooseTwiceChangesNothingTheSecondTime(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	path := filepath.Join(home, ".config", "goose", "config.yaml")
	if _, err := captureRun(t, "install", "goose", "--no-index"); err != nil {
		t.Fatalf("install goose: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "goose", "--no-index")
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("a repeat install rewrote config.yaml:\n--- first\n%s\n--- second\n%s", first, second)
	}
	// And it says so: an "updated" nobody can point at a change for is the
	// half of this that a reader sees.
	for _, line := range []string{"goose: command unchanged", "goose: unchanged"} {
		if !strings.Contains(out, line) {
			t.Errorf("a repeat install did not report %q:\n%s", line, out)
		}
	}
}
