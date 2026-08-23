package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
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
		"extensions/kimi/skills/deja-history/SKILL.md",
	} {
		got := string(repoFile(t, p))
		if want := guidanceText("claude"); got != want {
			t.Fatalf("%s has drifted from guidanceText:\n--- file ---\n%s\n--- installer ---\n%s", p, got, want)
		}
	}
}

// The plugin's own hooks are what a marketplace install gets instead of
// `deja install codex-auto`. Codex discovers them at hooks/hooks.json and
// expands ${PLUGIN_ROOT}; a path it cannot resolve is a hook that never runs.
func TestCodexPluginHooks(t *testing.T) {
	var file struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(repoFile(t, "codex-plugin/hooks/hooks.json"), &file); err != nil {
		t.Fatalf("hooks.json: %v", err)
	}
	want := map[string]string{
		"SessionStart":     "hook-context",
		"UserPromptSubmit": "hook-prompt",
		"PreCompact":       "hook-precompact",
	}
	for event, sub := range want {
		groups, ok := file.Hooks[event]
		if !ok || len(groups) == 0 || len(groups[0].Hooks) == 0 {
			t.Fatalf("no %s hook in the plugin", event)
		}
		cmd := groups[0].Hooks[0].Command
		if !strings.HasPrefix(cmd, "${PLUGIN_ROOT}/hooks/deja.sh ") || !strings.HasSuffix(cmd, sub) {
			t.Fatalf("%s runs %q, not the plugin's own script with %s", event, cmd, sub)
		}
		if groups[0].Hooks[0].Type != "command" || groups[0].Hooks[0].Timeout <= 0 {
			t.Fatalf("%s entry is not a command with a timeout: %+v", event, groups[0].Hooks[0])
		}
	}

	info, err := os.Stat(filepath.Join("..", "..", "codex-plugin", "hooks", "deja.sh"))
	if err != nil {
		t.Fatalf("the script the hooks point at is missing: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("codex-plugin/hooks/deja.sh is not executable (%v) — Codex runs it directly", info.Mode())
	}

	var manifest struct {
		Hooks []string `json:"hooks"`
	}
	if err := json.Unmarshal(repoFile(t, "codex-plugin/.codex-plugin/plugin.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Hooks) != 1 || manifest.Hooks[0] != "./hooks/hooks.json" {
		t.Fatalf("the manifest does not declare the hooks file: %v", manifest.Hooks)
	}
}

// The plugin stands down when `deja install codex-auto` has already written
// the same hook into Codex's own hooks.json. If the installer's command stops
// matching what the script greps for, both fire and the digest arrives twice.
func TestCodexPluginStandsDownForTheInstaller(t *testing.T) {
	script := string(repoFile(t, "codex-plugin/hooks/deja.sh"))
	if !strings.Contains(script, "CODEX_HOME") {
		t.Fatal("the script does not honour CODEX_HOME, so a relocated Codex home hides the installer's wiring")
	}
	pattern := regexp.MustCompile(`deja[^"]*hook-(context|prompt|precompact)`)
	written, err := json.Marshal(map[string]any{"command": "/opt/homebrew/bin/deja hook-context"})
	if err != nil {
		t.Fatal(err)
	}
	if !pattern.MatchString(string(written)) {
		t.Fatalf("the script's own pattern does not match what the installer writes: %s", written)
	}
}

// The plugin directory asks for these before it will list anything, and a
// missing logo or policy URL is a submission bounced weeks later rather than a
// build that fails now.
func TestCodexPluginListingMetadata(t *testing.T) {
	var manifest struct {
		Interface struct {
			DisplayName      string   `json:"displayName"`
			ShortDescription string   `json:"shortDescription"`
			LongDescription  string   `json:"longDescription"`
			DeveloperName    string   `json:"developerName"`
			Category         string   `json:"category"`
			WebsiteURL       string   `json:"websiteURL"`
			PrivacyPolicyURL string   `json:"privacyPolicyUrl"`
			TermsURL         string   `json:"termsOfServiceUrl"`
			Logo             string   `json:"logo"`
			LogoDark         string   `json:"logoDark"`
			DefaultPrompt    []string `json:"defaultPrompt"`
		} `json:"interface"`
	}
	if err := json.Unmarshal(repoFile(t, "codex-plugin/.codex-plugin/plugin.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	i := manifest.Interface
	for name, value := range map[string]string{
		"displayName":       i.DisplayName,
		"shortDescription":  i.ShortDescription,
		"longDescription":   i.LongDescription,
		"developerName":     i.DeveloperName,
		"category":          i.Category,
		"websiteURL":        i.WebsiteURL,
		"privacyPolicyUrl":  i.PrivacyPolicyURL,
		"termsOfServiceUrl": i.TermsURL,
	} {
		if strings.TrimSpace(value) == "" {
			t.Fatalf("interface.%s is empty", name)
		}
	}
	if len(i.DefaultPrompt) == 0 {
		t.Fatal("no starter prompts: the listing shows them, and the review asks for realistic ones")
	}
	// The logos ship inside the plugin, so a marketplace copy carries them.
	for _, rel := range []string{i.Logo, i.LogoDark} {
		if !strings.HasPrefix(rel, "./") {
			t.Fatalf("logo %q must be a ./ path inside the plugin", rel)
		}
		if _, err := os.Stat(filepath.Join("..", "..", "codex-plugin", strings.TrimPrefix(rel, "./"))); err != nil {
			t.Fatalf("logo %s is not in the plugin: %v", rel, err)
		}
	}
}
