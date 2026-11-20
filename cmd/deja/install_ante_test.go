package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnteInstallAndUninstall(t *testing.T) {
	home := hermeticEnv(t)
	anteHome := filepath.Join(t.TempDir(), "ante-home")
	t.Setenv("ANTE_HOME", anteHome)

	const exe = "/opt/deja/bin/deja"
	wantSettings := filepath.Join(anteHome, "settings.json")
	if got := anteSettingsPath(); got != wantSettings {
		t.Fatalf("anteSettingsPath() = %q, want %q", got, wantSettings)
	}

	// Preserve existing Ante settings alongside mcp_servers.
	if err := os.MkdirAll(anteHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wantSettings, []byte("{\n  \"model\": \"claude-sonnet-4-5\"\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r, err := installAnte(exe, false)
	if err != nil {
		t.Fatalf("installAnte: %v", err)
	}
	if r.Action != "updated" || r.Path != wantSettings {
		t.Fatalf("installAnte result = %#v", r)
	}

	var cfg struct {
		Model      string                    `json:"model"`
		MCPServers map[string]map[string]any `json:"mcp_servers"`
	}
	b, err := os.ReadFile(wantSettings)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "claude-sonnet-4-5" {
		t.Fatalf("existing model lost: %q", cfg.Model)
	}
	entry, ok := cfg.MCPServers["deja"]
	if !ok {
		t.Fatalf("missing mcp_servers.deja in %s", b)
	}
	if entry["command"] != exe {
		t.Fatalf("command = %v, want %q", entry["command"], exe)
	}

	// Idempotent second install.
	r2, err := installAnte(exe, false)
	if err != nil {
		t.Fatalf("second installAnte: %v", err)
	}
	if r2.Action != "unchanged" {
		t.Fatalf("second install Action = %q, want unchanged", r2.Action)
	}

	// CLI install writes shared skill beside settings.json.
	if _, err := captureRun(t, "install", "ante", "--no-index"); err != nil {
		t.Fatalf("deja install ante: %v", err)
	}
	skillPath := filepath.Join(home, "home", ".agents", "skills", "deja-history", "SKILL.md")
	if got := guidancePath("ante"); got != skillPath {
		t.Fatalf("guidancePath(ante) = %q, want %q", got, skillPath)
	}
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("missing shared skill %s: %v", skillPath, err)
	}

	// Uninstall removes deja entry while keeping other keys and mcp_servers absent if added by deja.
	if _, err := captureRun(t, "uninstall", "ante"); err != nil {
		t.Fatalf("deja uninstall ante: %v", err)
	}
	b, err = os.ReadFile(wantSettings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "deja") {
		t.Fatalf("settings.json still contains deja after uninstall: %s", b)
	}
	if !strings.Contains(string(b), `"model"`) {
		t.Fatalf("uninstall removed unrelated settings: %s", b)
	}

	// Malformed settings.json propagates error.
	if err := os.WriteFile(wantSettings, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installAnte(exe, false); err == nil {
		t.Fatal("expected error on malformed settings.json")
	}
}
