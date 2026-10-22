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
	t.Chdir(wd)
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

// The same rule held for the wiring record but not for the rest of deja's own
// directory: forget put the tombstone list, and install aider and rules sync
// aider's context file, under relcfg/ in the working directory (#5052).
func TestRelativeXDGConfigHomeIsIgnoredByEveryConfigFile(t *testing.T) {
	tmp := hermeticEnv(t)
	writeClaudeTurn(t, filepath.Join(tmp, "claude", "w", "sess-1.jsonl"), "sess-1", "a turn to forget")
	home := os.Getenv("HOME")
	wd := t.TempDir()
	t.Chdir(wd)
	t.Setenv("XDG_CONFIG_HOME", "relcfg")

	if _, err := captureRun(t, "install", "aider", "--no-index"); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(home, ".config", "deja")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "rules.md"), []byte("Use tabs.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "rules", "sync"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "forget", "--session", "sess-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wd, "relcfg")); err == nil {
		t.Fatal("a command created relcfg/ in the working directory")
	}
	ctx, err := os.ReadFile(filepath.Join(cfg, "aider-context.md"))
	if err != nil || !strings.Contains(string(ctx), "Use tabs.") {
		t.Fatalf("aider's context file under the home directory: %q, %v", ctx, err)
	}
	if conf, _ := os.ReadFile(filepath.Join(home, ".aider.conf.yml")); strings.Contains(string(conf), "relcfg") {
		t.Fatalf("aider was pointed at the relative directory:\n%s", conf)
	}
	if _, err := os.Stat(filepath.Join(cfg, "tombstones")); err != nil {
		t.Fatalf("the tombstone list is not under the home directory: %v", err)
	}
}
