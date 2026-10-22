package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runPluginBridge runs a plugin's hook bridge with a deja on PATH that only
// leaves a mark, and reports whether the bridge called it.
func runPluginBridge(t *testing.T, script string) bool {
	t.Helper()
	bin := t.TempDir()
	mark := filepath.Join(t.TempDir(), "ran")
	fake := "#!/bin/sh\ncat >/dev/null\n: > " + shellQuote(mark) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "deja"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "sh", filepath.Join("..", "..", script), "hook-context")
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	cmd.Stdin = strings.NewReader("{}")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v %s", script, err, out)
	}
	_, err := os.Stat(mark)
	return err == nil
}

// The plugins' bridges stand down when `deja install <h>-auto` already wrote
// the same hooks, or every hook runs twice. They looked for `deja hook-`, and
// the installer writes the launcher, `…/deja-hook hook-context`, so they never
// did (#4706).
func TestPluginBridgesStandDownBesideTheInstaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the bridges are sh scripts")
	}
	for _, c := range []struct {
		script, target string
	}{
		{"codebuddy-plugin/hooks/deja.sh", "codebuddy-auto"},
		{"claude-plugin/hooks/deja.sh", "claude-auto"},
		{"devin-plugin/hooks/deja.sh", "devin-auto"},
	} {
		t.Run(c.target, func(t *testing.T) {
			hermeticEnv(t)
			if !runPluginBridge(t, c.script) {
				t.Fatal("with nothing installed the bridge did not run deja")
			}
			exe := filepath.Join(t.TempDir(), "deja")
			if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := installTarget(c.target, exe, false); err != nil {
				t.Fatal(err)
			}
			if runPluginBridge(t, c.script) {
				t.Fatalf("%s wired the hooks and the plugin ran them too", c.target)
			}
		})
	}
}
