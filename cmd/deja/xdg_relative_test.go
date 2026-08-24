package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The XDG spec says a relative path in one of these variables is invalid and
// must be ignored. deja honoured it, so `XDG_CONFIG_HOME=relcfg deja install`
// wrote its wiring record into the directory the command was run from — where
// no later run would look for it (#1693).
func TestRelativeXDGConfigHomeIsIgnored(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	wd := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(wd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	t.Setenv("XDG_CONFIG_HOME", "relcfg")

	if _, err := captureRun(t, "install", "claude-code"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wd, "relcfg")); err == nil {
		t.Error("install created relcfg/ in the working directory")
	}
	want := filepath.Join(home, ".config", "deja", "wiring.json")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("the wiring record is not at the spec's fallback %s: %v", want, err)
	}
	if got := wiringStatePath(); !strings.HasPrefix(got, home) {
		t.Errorf("wiringStatePath() is %q, outside the home directory", got)
	}
}

// An absolute value is still honoured, spaces and all.
func TestAbsoluteXDGConfigHomeIsHonoured(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(t.TempDir(), "my cfg")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", base)

	if _, err := captureRun(t, "install", "claude-code"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, "deja", "wiring.json")); err != nil {
		t.Errorf("an absolute XDG_CONFIG_HOME was not used: %v", err)
	}
}
