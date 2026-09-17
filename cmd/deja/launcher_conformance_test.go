package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every generated hook or plugin names the launcher, never the binary the
// install happened to run from. That is what the launcher is for (#3422), and
// nine writers were still baking the path — so on a machine where deja had
// been built a few times, eleven harnesses were running hooks that pointed at
// builds in a scratch directory, and the day that directory is cleaned they
// exit 127 with nothing said (#3682).
//
// One test over every auto target, so the next writer cannot slip: an install
// with a distinctive path must leave that path out of the file and the
// launcher in it.
func TestEveryGeneratedHookNamesTheLauncher(t *testing.T) {
	if dejaLauncherPath := dejaLauncherPath(); dejaLauncherPath == "" {
		t.Skip("no launcher on this platform, so a hook has to name the binary")
	}
	for _, w := range autoWirings() {
		t.Run(w.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
			forgetWrittenExes()
			t.Cleanup(forgetWrittenExes)

			exe := filepath.Join(home, "builds", "deja-probe-2026")
			if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := installTarget(w.name+"-auto", exe, false); err != nil {
				// A harness that is not installed here writes nothing, and
				// that is not this test's business.
				t.Skipf("%s-auto: %v", w.name, err)
			}
			path := w.path()
			b, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("no file at %s", path)
			}
			text := string(b)
			if !strings.Contains(text, "hook-") && !strings.Contains(text, "hook_") {
				t.Skipf("%s is not a hook file", path)
			}
			// Per hook line, not per file: qwen, crush and zcode keep the MCP
			// server in the same file, and that entry names the binary on
			// purpose — it is the hook commands that have to go through the
			// launcher.
			for _, line := range strings.Split(text, "\n") {
				if !strings.Contains(line, "hook-") {
					continue
				}
				if strings.Contains(line, exe) {
					t.Errorf("%s runs a hook through the build it was installed from — it stops working the day that build moves:\n%s",
						path, strings.TrimSpace(line))
				}
			}
			if !strings.Contains(text, dejaLauncherPath()) {
				t.Errorf("%s does not name the launcher:\n%s", path, text)
			}
		})
	}
}
