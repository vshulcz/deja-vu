package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func grokUserSettings(t *testing.T, home string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".grok", "user-settings.json"))
	if err != nil {
		t.Fatalf("user-settings.json missing: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("user-settings.json: %v", err)
	}
	return root
}

func grokServers(t *testing.T, home string) []any {
	t.Helper()
	root := grokUserSettings(t, home)
	mcp, _ := root["mcp"].(map[string]any)
	servers, _ := mcp["servers"].([]any)
	return servers
}

// config.toml alone left grok-dev users with an integration that wrote a file
// nothing reads.
func TestInstallGrokWritesBothConfigShapes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installGrok("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".grok", "config.toml"))
	if err != nil {
		t.Fatalf("config.toml missing: %v", err)
	}
	if !strings.Contains(string(b), "[mcp_servers.deja]") {
		t.Fatalf("config.toml has no deja block:\n%s", b)
	}
	servers := grokServers(t, home)
	if len(servers) != 1 {
		t.Fatalf("user-settings.json has %d servers, want ours", len(servers))
	}
	m, _ := servers[0].(map[string]any)
	// grok-dev keys servers by id and skips anything not enabled.
	for _, k := range []string{"id", "label", "enabled", "transport", "command"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("server entry missing %q: %v", k, m)
		}
	}
	if m["transport"] != "stdio" {
		t.Fatalf("transport = %v", m["transport"])
	}
	if m["enabled"] != true {
		t.Fatalf("server is written disabled: %v", m)
	}
}

func TestInstallGrokKeepsOtherServersAndSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".grok")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"defaultModel":"grok-4","mcp":{"servers":[{"id":"other","label":"other","enabled":true,"transport":"stdio","command":"/bin/other"}]}}`
	if err := os.WriteFile(filepath.Join(dir, "user-settings.json"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGrok("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if got := len(grokServers(t, home)); got != 2 {
		t.Fatalf("servers = %d, install dropped the user's own", got)
	}
	if grokUserSettings(t, home)["defaultModel"] != "grok-4" {
		t.Fatal("install rewrote unrelated settings")
	}
	if _, err := installGrok("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	servers := grokServers(t, home)
	if len(servers) != 1 {
		t.Fatalf("uninstall left %d servers, want the user's one", len(servers))
	}
	if m, _ := servers[0].(map[string]any); m["id"] != "other" {
		t.Fatalf("uninstall removed the wrong server: %v", servers)
	}
}

// The maintained CLI dropped GROK.md for AGENTS.md; writing only the old name
// is guidance nothing reads.
func TestGrokGuidanceLandsInBothFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installGuidance("grok", false); err != nil {
		t.Fatalf("guidance: %v", err)
	}
	for _, name := range []string{"GROK.md", "AGENTS.md"} {
		b, err := os.ReadFile(filepath.Join(home, ".grok", name))
		if err != nil {
			t.Fatalf("%s missing: %v", name, err)
		}
		if !strings.Contains(string(b), guidanceStart) {
			t.Fatalf("%s has no deja block:\n%s", name, b)
		}
	}
	if _, err := installGuidance("grok", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".grok", "AGENTS.md"))
	if strings.Contains(string(b), guidanceStart) {
		t.Fatalf("uninstall left the block in AGENTS.md:\n%s", b)
	}
}
