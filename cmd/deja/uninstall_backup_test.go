package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Uninstalling must not leave a copy of deja's own wiring behind. The snapshot
// is taken while the config is edited, and on the way out that config is the
// installed one — so the .bak held deja's hooks and a path to a binary the user
// had just removed, sitting in their config directory. Found by sweeping
// install-then-uninstall across every harness.
func TestUninstallTakesItsOwnBackupBackOut(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	installed := []byte(`{"hooks":{"UserPromptSubmit":[{"command":"/usr/local/bin/deja hook-prompt"}]}}`)
	if err := os.WriteFile(path, installed, 0o644); err != nil {
		t.Fatal(err)
	}

	removingWiring = true
	defer func() { removingWiring = false }()
	if _, err := writeIfChanged(path, installed, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		b, _ := os.ReadFile(path + ".bak")
		t.Fatalf("uninstall left a snapshot of deja's own wiring:\n%s", b)
	}
	live, err := os.ReadFile(path)
	if err != nil || string(live) != "{}" {
		t.Fatalf("the live config was not cleaned: %q %v", live, err)
	}
}

// A .bak the user made is theirs. deja only removes the one it created in the
// same call, so an existing snapshot survives untouched.
func TestUninstallKeepsABackupItDidNotMake(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("theirs = 1\n[hooks]\ncommand = \"/usr/local/bin/deja hook-prompt\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mine := "# my own snapshot from before any of this\n"
	if err := os.WriteFile(path+".bak", []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}

	removingWiring = true
	defer func() { removingWiring = false }()
	if _, err := writeIfChanged(path, []byte("old"), []byte("theirs = 1\n")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("the user's own backup was removed: %v", err)
	}
	if !strings.Contains(string(b), "my own snapshot") {
		t.Fatalf("the user's own backup was overwritten:\n%s", b)
	}
}

// Installing still leaves a snapshot: that is the point of taking one.
func TestInstallKeepsItsBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	theirs := []byte(`{"theirs":true}`)
	if err := os.WriteFile(path, theirs, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeIfChanged(path, theirs, []byte(`{"theirs":true,"hooks":{}}`)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("install did not keep a snapshot of what it edited: %v", err)
	}
	if string(b) != string(theirs) {
		t.Fatalf("the snapshot is not what was there before:\n%s", b)
	}
}
