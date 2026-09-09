package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// hookExeInConfigs is what a hook entry names after an install from exe: the
// launcher where deja can write one, and the binary itself where it cannot
// (#3422). Tests that used to compare against the binary path go through this.
func hookExeInConfigs(exe string) string {
	if p := dejaLauncherPath(); p != "" {
		return p
	}
	return exe
}

// dejaWiringPresent reports whether an install left something of deja's in a
// config: the binary for an MCP entry, the launcher for a hook (#3422).
func dejaWiringPresent(text, exe string) bool {
	return strings.Contains(text, exe) || strings.Contains(text, hookExeInConfigs(exe))
}

// The whole point of the launcher: the binary moves and the hook keeps
// working, with no config rewritten. Run for real, because a resolver that is
// only read is a resolver nobody has run.
func TestTheLauncherFindsTheBinaryAfterItMoves(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no launcher on windows — the entry names the binary, as before")
	}
	tmp := hermeticEnv(t)
	// This machine's own deja is not part of what is being tested, and it sits
	// in one of the well-known places.
	saved := launcherWellKnown
	t.Cleanup(func() { launcherWellKnown = saved })
	launcherWellKnown = nil
	first := filepath.Join(tmp, "old", "deja")
	if err := os.MkdirAll(filepath.Dir(first), 0o755); err != nil {
		t.Fatal(err)
	}
	// Stands in for deja: it says which build answered.
	if err := os.WriteFile(first, []byte("#!/bin/sh\necho old \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	launcher, err := writeDejaLauncher(first)
	if err != nil {
		t.Fatal(err)
	}

	if out := runLauncher(t, launcher, nil); !strings.HasPrefix(out, "old hook-prompt") {
		t.Fatalf("the launcher did not run the installed binary: %q", out)
	}

	// The upgrade: same name, somewhere else, and nothing has rewritten a
	// config. It is found on the PATH, which is where an upgrade leaves it.
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	next := filepath.Join(tmp, "new", "deja")
	if err := os.MkdirAll(filepath.Dir(next), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(next, []byte("#!/bin/sh\necho new \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out := runLauncher(t, launcher, []string{"PATH=" + filepath.Dir(next)}); !strings.HasPrefix(out, "new hook-prompt") {
		t.Fatalf("the launcher did not follow the binary: %q", out)
	}

	// DEJA_BIN wins over both, for a machine that keeps deja somewhere of its
	// own.
	pinned := filepath.Join(tmp, "pinned-deja")
	if err := os.WriteFile(pinned, []byte("#!/bin/sh\necho pinned \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out := runLauncher(t, launcher, []string{"DEJA_BIN=" + pinned, "PATH=" + filepath.Dir(next)}); !strings.HasPrefix(out, "pinned hook-prompt") {
		t.Fatalf("DEJA_BIN did not win: %q", out)
	}

	// And when there is no deja anywhere, silence and a zero exit: a hook that
	// cannot find its binary has no memory to add, and a failing hook is
	// something harnesses report at the reader every turn.
	out, err := exec.Command(launcher, "hook-prompt").Output()
	if err == nil && strings.TrimSpace(string(out)) != "" {
		t.Fatalf("a launcher with nothing to run said %q", out)
	}
}

func runLauncher(t *testing.T, launcher string, env []string) string {
	t.Helper()
	cmd := exec.Command(launcher, "hook-prompt")
	cmd.Env = append([]string{"PATH=/nonexistent"}, env...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("launcher: %v", err)
	}
	return string(out)
}

// An install points the configs at the launcher rather than at the build it
// ran from, which is the whole of what makes an upgrade cheap.
func TestInstallWiresHooksThroughTheLauncher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no launcher on windows")
	}
	hermeticEnv(t)
	if _, err := installTarget("claude-auto", "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	settings := readFile(t, filepath.Join(sources.ClaudeConfigDir(), "settings.json"))
	if !strings.Contains(settings, dejaLauncherPath()+" hook-prompt") {
		t.Errorf("the hook does not run the launcher:\n%s", settings)
	}
	if _, err := os.Stat(dejaLauncherPath()); err != nil {
		t.Fatalf("the launcher the config names is not there: %v", err)
	}
	// The MCP entry is spawned by the client rather than by a shell, so it
	// keeps naming the binary.
	if mcp := readFile(t, sources.ClaudeJSONPath()); strings.Contains(mcp, "deja-hook") {
		t.Errorf("the MCP entry was pointed at the launcher:\n%s", mcp)
	}
}

// Uninstalling the last target takes the launcher with it; uninstalling one of
// several leaves it, because the others still run it.
func TestUninstallRemovesTheLauncherWhenNothingIsLeft(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no launcher on windows")
	}
	tmp := hermeticEnv(t)
	if err := os.MkdirAll(filepath.Join(tmp, "home", ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "codex-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "claude-auto"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dejaLauncherPath()); err != nil {
		t.Fatalf("the launcher went while codex was still running it: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "codex-auto"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dejaLauncherPath()); err == nil {
		t.Error("the launcher outlived the wiring that pointed at it")
	}
}
