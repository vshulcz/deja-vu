package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoFile(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	// Git may check these out with CRLF on Windows; the comparison is about
	// content, not about how the working tree stores it.
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

// The Claude Code plugin is what `claude plugin install` pulls, so a typo in
// either manifest breaks the one-command install for everyone.
func TestClaudePluginManifestsAreWellFormed(t *testing.T) {
	var market struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(repoFile(t, ".claude-plugin/marketplace.json"), &market); err != nil {
		t.Fatalf("marketplace.json: %v", err)
	}
	if len(market.Plugins) != 1 || market.Plugins[0].Source != "./claude-plugin" {
		t.Fatalf("marketplace does not point at the plugin directory: %+v", market.Plugins)
	}
	var plugin struct {
		Name  string `json:"name"`
		Hooks map[string][]struct {
			Hooks []struct {
				Command       string `json:"command"`
				StatusMessage string `json:"statusMessage"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(repoFile(t, "claude-plugin/.claude-plugin/plugin.json"), &plugin); err != nil {
		t.Fatalf("plugin.json: %v", err)
	}
	if plugin.Name != market.Plugins[0].Name {
		t.Fatalf("plugin name %q does not match the marketplace entry %q", plugin.Name, market.Plugins[0].Name)
	}
	// The same three events the local installer wires, so a plugin user is
	// not quietly getting less than someone who ran `deja install`.
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreCompact"} {
		groups, ok := plugin.Hooks[event]
		if !ok || len(groups) == 0 || len(groups[0].Hooks) == 0 {
			t.Fatalf("plugin does not hook %s", event)
		}
		h := groups[0].Hooks[0]
		if !strings.HasPrefix(h.Command, "${CLAUDE_PLUGIN_ROOT}/hooks/deja.sh ") {
			t.Fatalf("%s command is not plugin-relative: %q", event, h.Command)
		}
		// Without this the pause before the first prompt is unexplained.
		if h.StatusMessage == "" {
			t.Fatalf("%s hook has no statusMessage", event)
		}
	}
}

// Five registries install this one bundle, and in four of them it is the only
// thing the user runs — so the recall tools have to come with it rather than
// waiting for a separate `deja install`.
func TestPluginBundleShipsTheMCPServer(t *testing.T) {
	var plugin struct {
		MCPServers map[string]struct {
			Command string `json:"command"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(repoFile(t, "claude-plugin/.claude-plugin/plugin.json"), &plugin); err != nil {
		t.Fatalf("plugin.json: %v", err)
	}
	server, ok := plugin.MCPServers["deja"]
	if !ok {
		t.Fatalf("bundle declares no MCP server: %+v", plugin.MCPServers)
	}
	// An absolute path from the author's machine, or a bare `deja` that is not
	// on the client's PATH, both register a server that never starts.
	if !strings.HasPrefix(server.Command, "${CLAUDE_PLUGIN_ROOT}/") {
		t.Fatalf("MCP command is not plugin-relative: %q", server.Command)
	}
	launcher := string(repoFile(t, "claude-plugin/mcp/deja-mcp.sh"))
	if !strings.Contains(launcher, "mcp") {
		t.Fatalf("launcher does not start the MCP server:\n%s", launcher)
	}
	// The hook bridge stands down when deja is installed locally; the MCP
	// launcher must not copy that, or the server exits at handshake.
	if strings.Contains(launcher, "settings.json") {
		t.Fatal("MCP launcher stands down like the hook bridge; clients dedupe by name instead")
	}
	if strings.Contains(launcher, "exit 0") {
		t.Fatal("launcher exits clean without a binary; the client would report a server that answers nothing")
	}
}

// The plugin ships its own copy of the slash command; it must not drift from
// the one `deja install` writes.
func TestPluginCommandMatchesInstaller(t *testing.T) {
	want := claudeCommandMD("deja")
	got := string(repoFile(t, "claude-plugin/commands/deja.md"))
	if got != want {
		t.Fatalf("claude-plugin/commands/deja.md has drifted from claudeCommandMD:\n--- file ---\n%s\n--- installer ---\n%s", got, want)
	}
}

// A plugin user may not have the binary yet. The bridge must say so through
// the hook's own channel and still exit clean.
func TestPluginBridgeHandlesMissingBinary(t *testing.T) {
	script := string(repoFile(t, "claude-plugin/hooks/deja.sh"))
	for _, want := range []string{"systemMessage", "$HOME/.local/bin/deja", "exit 0", `exec "$DEJA"`} {
		if !strings.Contains(script, want) {
			t.Fatalf("bridge script missing %q", want)
		}
	}
}

// Users who already ran `deja install claude-auto` and then install the
// plugin must not get the digest twice.
func TestPluginBridgeStandsDownWhenLocallyInstalled(t *testing.T) {
	script := string(repoFile(t, "claude-plugin/hooks/deja.sh"))
	if !strings.Contains(script, `grep -q "deja hook-"`) {
		t.Fatalf("bridge does not detect an existing local install:\n%s", script)
	}
	if !strings.Contains(script, "CLAUDE_CONFIG_DIR") {
		t.Fatal("bridge ignores CLAUDE_CONFIG_DIR when looking for settings.json")
	}
}
