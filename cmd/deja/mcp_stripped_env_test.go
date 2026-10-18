package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// OpenClaw, Codex, Hermes and Grok start an MCP server with HOME, PATH and a
// few more, so a `deja mcp` named directly lost DEJA_INDEX_DIR, opened a
// second index under ~/.cache/deja and answered recall with the asking session
// (#5269, #5124). Run the command each config names under that stripped
// environment and see which index it gets.
func TestMCPEntriesOfStrippingClientsKeepTheIndexDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no launcher on windows")
	}
	tmp := hermeticEnv(t)
	saved := launcherWellKnown
	t.Cleanup(func() { launcherWellKnown = saved })
	launcherWellKnown = nil
	idx := filepath.Join(tmp, "moved", "index.db")
	t.Setenv("DEJA_INDEX_DIR", idx)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg-config"))
	t.Setenv("OPENCLAW_STATE_DIR", filepath.Join(tmp, "openclaw"))
	exe := filepath.Join(tmp, "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho \"args=$*\"\necho \"DEJA_INDEX_DIR=$DEJA_INDEX_DIR\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	for name, c := range map[string]struct {
		install func(string, bool) (installResult, error)
		path    func() string
	}{
		"openclaw": {installOpenClawMCP, func() string { return filepath.Join(sources.OpenClawStateDir(), "openclaw.json") }},
		"codex":    {installCodex, func() string { return filepath.Join(sources.CodexHome(), "config.toml") }},
		"hermes":   {installHermesMCP, func() string { return filepath.Join(sources.HermesHome(), "config.yaml") }},
		"grok":     {installGrokConfig, func() string { return filepath.Join(sources.GrokHome(), "config.toml") }},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := c.install(exe, false); err != nil {
				t.Fatal(err)
			}
			text := readFile(t, c.path())
			command := dejaKeyedCommand(text)
			if name == "openclaw" {
				var root map[string]any
				if err := json.Unmarshal([]byte(text), &root); err != nil {
					t.Fatal(err)
				}
				command = mcpEntryCommand(jsonAt(root, "mcp", "servers", "deja"))
			}
			if command == "" {
				t.Fatalf("no command in the deja entry:\n%s", text)
			}
			cmd := exec.Command(command, "mcp")
			cmd.Env = []string{"HOME=" + filepath.Join(tmp, "home"), "PATH=/usr/bin:/bin"}
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("%s: %v", command, err)
			}
			if !strings.Contains(string(out), "DEJA_INDEX_DIR="+idx+"\n") || !strings.Contains(string(out), "args=mcp\n") {
				t.Errorf("%s under a stripped env ran with\n%s\nwant DEJA_INDEX_DIR=%s", command, out, idx)
			}

			if _, err := c.install(exe, true); err != nil {
				t.Fatal(err)
			}
			if after := readFile(t, c.path()); strings.Contains(after, "deja-hook") {
				t.Errorf("uninstall left the launcher entry:\n%s", after)
			}
		})
	}
}
