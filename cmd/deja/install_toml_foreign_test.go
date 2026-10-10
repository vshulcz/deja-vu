package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The TOML side of TestUninstallKeepsTheSnapshotOfAForeignDejaEntry: a
// [mcp_servers.deja] of the reader's own is replaced on install, so after the
// uninstall it has to survive in the config or in its snapshot.
func TestUninstallKeepsTheSnapshotOfAForeignTOMLDejaEntry(t *testing.T) {
	for _, tc := range []struct {
		target string
		path   func(home string) string
	}{
		{"codex", func(h string) string { return filepath.Join(h, ".codex", "config.toml") }},
		{"grok", func(h string) string { return filepath.Join(h, ".grok", "config.toml") }},
		{"trae", func(string) string { return traeConfigPath() }},
	} {
		t.Run(tc.target, func(t *testing.T) {
			home := regressHome(t)
			path := tc.path(home)
			orig := "model = \"x\"\n\n[mcp_servers.deja]\ncommand = \"/usr/local/bin/other-tool\"\nargs = [\"serve\"]\n\n[mcp_servers.deja.env]\nK = \"V\"\n"
			regressWrite(t, path, orig)
			if err := regressRun(t, false, tc.target, "--no-index"); err != nil {
				t.Fatal(err)
			}
			if err := regressRun(t, true, tc.target); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(path)
			bak, _ := os.ReadFile(path + ".bak")
			if !strings.Contains(string(got), "other-tool") && !strings.Contains(string(bak), "other-tool") {
				t.Fatalf("the user's own [mcp_servers.deja] is gone from both the config and its snapshot:\nconfig: %s\nbak: %q", got, bak)
			}
		})
	}
}

// kimi and codewhale keep their servers in JSON beside a TOML config.
func TestUninstallKeepsAForeignDejaEntryBesideTOML(t *testing.T) {
	for _, tc := range []struct {
		target, key string
		path        func() string
	}{
		{"kimi", "mcpServers", func() string { return filepath.Join(sources.KimiConfigDir(), "mcp.json") }},
		{"codewhale", "servers", codewhaleMCPPath},
	} {
		t.Run(tc.target, func(t *testing.T) {
			regressHome(t)
			path := tc.path()
			regressWrite(t, path, "{\n  \""+tc.key+"\": {\n    \"deja\": {\"command\": \"/usr/local/bin/other-tool\", \"args\": [\"serve\"]}\n  }\n}\n")
			if err := regressRun(t, false, tc.target, "--no-index"); err != nil {
				t.Fatal(err)
			}
			if err := regressRun(t, true, tc.target); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(path)
			bak, _ := os.ReadFile(path + ".bak")
			if !strings.Contains(string(got), "other-tool") && !strings.Contains(string(bak), "other-tool") {
				t.Fatalf("the user's own \"deja\" server is gone:\nconfig: %s\nbak: %q", got, bak)
			}
		})
	}
}

// deja's own block, under any binary name, is still deja's to drop.
func TestHoldsForeignTOMLDejaEntry(t *testing.T) {
	for body, want := range map[string]bool{
		"[mcp_servers.deja]\ntype = \"stdio\"\ncommand = \"/opt/bin/deja\"\nargs = [\"mcp\"]\n":          false,
		"[mcp_servers.deja]\ncommand = \"/opt/bin/renamed\"\nargs = [\n  \"mcp\",\n]\n":                  false,
		"[mcp_servers.deja]\ncommand = \"cmd\"\nargs = [\"/c\", \"deja\", \"mcp\"]\n":                    false,
		"[mcp_servers.deja]\ncommand = \"/usr/local/bin/other-tool\"\nargs = [\"serve\"]\n":              true,
		"[mcp_servers.deja]\nurl = \"https://example.com/mcp\"\n":                                        true,
		"[mcp_servers.other]\ncommand = \"/usr/local/bin/other-tool\"\n\n[tui]\nstatusline = \"deja\"\n": false,
	} {
		if got := holdsForeignDejaEntry([]byte(body)); got != want {
			t.Errorf("holdsForeignDejaEntry(%q) = %v, want %v", body, got, want)
		}
	}
}
