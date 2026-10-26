package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Muse Code runs hooks with HOME, PATH and little else, so a hook lost the
// XDG_* and DEJA_* values the CLI ran with and read another index, another
// policy and another Muse store (#4738). The launcher carries them: run with a
// stripped environment, the hook sees what the install saw; run by a host that
// passes its own, the host's value wins.
func TestLauncherCarriesStoreLocationsIntoAStrippedHookEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no launcher on windows")
	}
	tmp := hermeticEnv(t)
	saved := launcherWellKnown
	t.Cleanup(func() { launcherWellKnown = saved })
	launcherWellKnown = nil
	data := filepath.Join(tmp, "xdg data")
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg-config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(tmp, "home", ".cache")) // the default: adds nothing
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	t.Setenv("DEJA_MUSE_ROOTS", filepath.Join(tmp, "it's muse"))
	t.Setenv("DEJA_EMBED_KEY", "sk-secret-4738")
	t.Setenv("DEJA_HERMES_PG_DSN", "postgres://u:pw-4738@h/db")
	t.Setenv("DEJA_TRACE", "1")

	exe := filepath.Join(tmp, "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nenv\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	launcher, err := writeDejaLauncher(exe)
	if err != nil {
		t.Fatal(err)
	}
	script := readFile(t, launcher)
	for _, leak := range []string{"sk-secret-4738", "pw-4738", "DEJA_TRACE", "XDG_CACHE_HOME", "DEJA_WARMUP_SENTINEL"} {
		if strings.Contains(script, leak) {
			t.Errorf("the launcher carries %s:\n%s", leak, script)
		}
	}

	run := func(env ...string) map[string]string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), launcher, "hook-prompt")
		cmd.Env = append([]string{"HOME=" + filepath.Join(tmp, "home"), "PATH=/usr/bin:/bin"}, env...)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("launcher: %v", err)
		}
		got := map[string]string{}
		for l := range strings.SplitSeq(string(out), "\n") {
			if k, v, ok := strings.Cut(l, "="); ok {
				got[k] = v
			}
		}
		return got
	}

	got := run()
	for k, want := range map[string]string{
		"XDG_DATA_HOME":          data,
		"XDG_CONFIG_HOME":        filepath.Join(tmp, "xdg-config"),
		"DEJA_INCLUDE_SUBAGENTS": "1",
		"DEJA_MUSE_ROOTS":        filepath.Join(tmp, "it's muse"),
		"DEJA_INDEX_DIR":         filepath.Join(tmp, "index.db"),
	} {
		if got[k] != want {
			t.Errorf("a stripped hook sees %s=%q, want %q", k, got[k], want)
		}
	}
	if _, ok := got["DEJA_EMBED_KEY"]; ok {
		t.Error("a key reached the hook through the launcher")
	}

	// A host that passes its own environment keeps it.
	got = run("XDG_DATA_HOME=/srv/own", "DEJA_INCLUDE_SUBAGENTS=0")
	if got["XDG_DATA_HOME"] != "/srv/own" || got["DEJA_INCLUDE_SUBAGENTS"] != "0" {
		t.Errorf("the launcher overrode the host's environment: XDG_DATA_HOME=%q DEJA_INCLUDE_SUBAGENTS=%q", got["XDG_DATA_HOME"], got["DEJA_INCLUDE_SUBAGENTS"])
	}

	// Same environment, same bytes: a second install does not rewrite it.
	before, _ := os.Stat(launcher)
	if _, err := writeDejaLauncher(exe); err != nil {
		t.Fatal(err)
	}
	if readFile(t, launcher) != script {
		t.Error("a second install with the same environment wrote a different launcher")
	}
	if after, _ := os.Stat(launcher); !after.ModTime().Equal(before.ModTime()) {
		t.Error("a second install rewrote an unchanged launcher")
	}
}

// With nothing moved, the launcher is what it was before #4738.
func TestLauncherCarriesNothingOnADefaultSetup(t *testing.T) {
	home := t.TempDir()
	env := []string{"HOME=" + home, "PATH=/bin", "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "XDG_DATA_HOME=relative", "TERM=xterm", "DEJA_DEBUG=1", "DEJA_PASS_HOME=1"}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if got := launcherEnv(env); len(got) != 0 {
		t.Errorf("a default setup carries %v", got)
	}
}

func TestLauncherCarriesGlobalRecallOptIn(t *testing.T) {
	got := launcherEnv([]string{"DEJA_RECALL_GLOBAL=1"})
	if len(got) != 1 || got[0] != [2]string{"DEJA_RECALL_GLOBAL", "1"} {
		t.Fatalf("launcher lost global recall setting: %v", got)
	}
}
