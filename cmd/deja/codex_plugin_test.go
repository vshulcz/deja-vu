package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Codex marketplace rejects the whole file on an unknown enum, so a typo
// here means `codex plugin marketplace add` fails for everyone.
func TestCodexMarketplaceManifest(t *testing.T) {
	var market struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source struct {
				Source string `json:"source"`
				Path   string `json:"path"`
			} `json:"source"`
			Policy struct {
				Installation   string `json:"installation"`
				Authentication string `json:"authentication"`
			} `json:"policy"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(repoFile(t, ".agents/plugins/marketplace.json"), &market); err != nil {
		t.Fatalf("marketplace.json: %v", err)
	}
	if len(market.Plugins) != 1 {
		t.Fatalf("want exactly one plugin, got %d", len(market.Plugins))
	}
	p := market.Plugins[0]
	// Paths resolve against the repository root, not the manifest directory.
	if p.Source.Path != "./codex-plugin" {
		t.Fatalf("source path = %q", p.Source.Path)
	}
	// Codex only accepts ON_INSTALL or ON_USE here; NONE is rejected outright.
	if p.Policy.Authentication != "ON_USE" && p.Policy.Authentication != "ON_INSTALL" {
		t.Fatalf("authentication policy %q is not one Codex accepts", p.Policy.Authentication)
	}
}

func TestCodexPluginManifest(t *testing.T) {
	var plugin struct {
		Name       string `json:"name"`
		Version    string `json:"version"`
		Skills     string `json:"skills"`
		MCPServers string `json:"mcpServers"`
		Interface  struct {
			DisplayName   string   `json:"displayName"`
			DefaultPrompt []string `json:"defaultPrompt"`
		} `json:"interface"`
	}
	if err := json.Unmarshal(repoFile(t, "codex-plugin/.codex-plugin/plugin.json"), &plugin); err != nil {
		t.Fatalf("plugin.json: %v", err)
	}
	if plugin.Version == "" || plugin.Name == "" {
		t.Fatalf("plugin manifest is missing name/version: %+v", plugin)
	}
	// The pointers must resolve, or the plugin installs as an empty shell.
	for _, rel := range []string{plugin.Skills, plugin.MCPServers} {
		if rel == "" {
			t.Fatal("plugin declares no skills or MCP servers")
		}
		if _, err := os.Stat(filepath.Join("..", "..", "codex-plugin", strings.TrimPrefix(rel, "./"))); err != nil {
			t.Fatalf("plugin points at %s which does not exist: %v", rel, err)
		}
	}
	// The composer surfaces these; an empty list wastes the slot.
	if len(plugin.Interface.DefaultPrompt) == 0 || plugin.Interface.DisplayName == "" {
		t.Fatalf("interface metadata is incomplete: %+v", plugin.Interface)
	}
	var mcp struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(repoFile(t, "codex-plugin/.mcp.json"), &mcp); err != nil {
		t.Fatalf(".mcp.json: %v", err)
	}
	s, ok := mcp.Servers["deja"]
	if !ok || s.Command != "deja" || len(s.Args) != 1 || s.Args[0] != "mcp" {
		t.Fatalf("mcp server entry = %+v", mcp.Servers)
	}
}

// Every bundled skill must be the same one `deja install` writes. Both plugin
// directories ship one, and a harness that reads the bundle gets whatever is in
// it — so a stale copy would teach an agent something the installed skill no
// longer says.
func TestBundledSkillsMatchInstaller(t *testing.T) {
	for _, p := range []string{
		"codex-plugin/skills/deja-history/SKILL.md",
		"claude-plugin/skills/deja-history/SKILL.md",
	} {
		got := string(repoFile(t, p))
		if want := guidanceText("claude"); got != want {
			t.Fatalf("%s has drifted from guidanceText:\n--- file ---\n%s\n--- installer ---\n%s", p, got, want)
		}
	}
}
