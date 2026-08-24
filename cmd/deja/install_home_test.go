package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// With HOME unset every filepath.Join(homeDir(), …) became a relative path, so
// install wrote .claude/, .claude.json and .config/deja/wiring.json into the
// working directory — a repository, usually — and reported success (#1690).
func TestInstallRefusesWhenHomeIsUnknown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("USERPROFILE and the profile APIs make an unset HOME a different question here")
	}
	hermeticEnv(t)
	t.Setenv("HOME", "")

	wd := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(wd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	_, err = captureRun(t, "install", "claude-code")
	if err == nil {
		t.Error("install accepted a run with no home directory")
	} else if !strings.Contains(err.Error(), "home") {
		t.Errorf("the refusal does not say what is missing: %s", err)
	}

	for _, name := range []string{".claude", ".claude.json", ".agents", ".config"} {
		if _, statErr := os.Stat(filepath.Join(wd, name)); statErr == nil {
			t.Errorf("install created %s in the working directory", name)
		}
	}
}

// The neighbouring case must keep working: a home that exists is not affected.
func TestInstallStillWorksWithAHome(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-code"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); err != nil {
		t.Errorf("install did not write into the real home: %v", err)
	}
}
