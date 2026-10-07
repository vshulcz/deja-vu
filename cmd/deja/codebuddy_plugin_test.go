package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The CodeBuddy plugin is what a marketplace install gets instead of
// `deja install codebuddy-auto`, so it has to carry the same wiring: the same
// events, matchers and hook subcommands, a server that starts, and files the
// manifest points at that exist. Its version moves with the other plugins.
func TestCodeBuddyPluginMatchesTheInstaller(t *testing.T) {
	var m struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Skills  string `json:"skills"`
		Hooks   map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(repoFile(t, "codebuddy-plugin/.codebuddy-plugin/plugin.json"), &m); err != nil {
		t.Fatal(err)
	}
	var claude struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(repoFile(t, "claude-plugin/.claude-plugin/plugin.json"), &claude); err != nil {
		t.Fatal(err)
	}
	if m.Name != "deja-vu" || m.Version == "" || m.Version != claude.Version {
		t.Fatalf("name %q version %q, want deja-vu at %q", m.Name, m.Version, claude.Version)
	}

	root := filepath.Join("..", "..", "codebuddy-plugin")
	const bridge = `"${CODEBUDDY_PLUGIN_ROOT}/hooks/deja.sh" `
	entries := 0
	for _, es := range m.Hooks {
		entries += len(es)
	}
	if entries != len(codeBuddyHookWiring) {
		t.Fatalf("plugin wires %d entries, the installer %d", entries, len(codeBuddyHookWiring))
	}
	for _, w := range codeBuddyHookWiring {
		found := false
		for _, e := range m.Hooks[w.Event] {
			if len(e.Hooks) != 1 || e.Hooks[0].Command != bridge+w.Sub {
				continue
			}
			found = true
			h := e.Hooks[0]
			if e.Matcher != w.Matcher || h.Type != "command" || h.Timeout != codeBuddyHookTimeout {
				t.Fatalf("%s: plugin entry %+v, installer matcher %q", w.Event, e, w.Matcher)
			}
		}
		if !found {
			t.Fatalf("%s: the plugin has no %q hook", w.Event, bridge+w.Sub)
		}
	}

	s, ok := m.MCPServers["deja"]
	if !ok || s.Command != "${CODEBUDDY_PLUGIN_ROOT}/mcp/deja-mcp.sh" {
		t.Fatalf("mcpServers = %+v", m.MCPServers)
	}
	for _, f := range []string{"hooks/deja.sh", "mcp/deja-mcp.sh"} {
		st, err := os.Stat(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && st.Mode()&0o111 == 0 {
			t.Fatalf("%s is not executable", f)
		}
	}
	if _, err := os.Stat(filepath.Join(root, strings.TrimPrefix(m.Skills, "./"), "deja-history", "SKILL.md")); err != nil {
		t.Fatalf("skills: %v", err)
	}
	// The bridge stands down when the installer already wired CodeBuddy, by
	// reading CodeBuddy's settings rather than Claude Code's.
	if b := string(repoFile(t, "codebuddy-plugin/hooks/deja.sh")); !strings.Contains(b, `${CODEBUDDY_CONFIG_DIR:-$HOME/.codebuddy}/settings.json`) {
		t.Fatal("the hook bridge does not check CodeBuddy's settings.json before running")
	}
}
