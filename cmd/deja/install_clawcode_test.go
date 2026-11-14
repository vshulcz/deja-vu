package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallClawCodeInstallIdempotentAndUninstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAW_CONFIG_HOME", "")

	settingsPath := filepath.Join(home, ".claw", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{
  "model": "claude-sonnet-4-6",
  "mcpServers": {
    "other": {"command": "other-mcp"}
  }
}
`
	if err := os.WriteFile(settingsPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := installClawCode("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	firstBytes, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]any
	if err = json.Unmarshal(firstBytes, &parsed); err != nil {
		t.Fatalf("parse settings after install: %v", err)
	}
	if parsed["model"] != "claude-sonnet-4-6" {
		t.Fatalf("lost unrelated setting model: %#v", parsed["model"])
	}
	servers, _ := parsed["mcpServers"].(map[string]any)
	if servers["other"] == nil || servers["deja"] == nil {
		t.Fatalf("expected both other and deja in mcpServers, got %#v", servers)
	}

	skillPath := filepath.Join(home, ".claw", "skills", "deja-history", "SKILL.md")
	if _, err = os.Stat(skillPath); err != nil {
		t.Fatalf("missing skill file %s: %v", skillPath, err)
	}
	cmdPath := filepath.Join(home, ".claw", "commands", "deja.md")
	if _, err = os.Stat(cmdPath); err != nil {
		t.Fatalf("missing command file %s: %v", cmdPath, err)
	}

	if _, err = installClawCode("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("re-install: %v", err)
	}
	secondBytes, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("installClawCode not idempotent:\n--- first ---\n%s\n--- second ---\n%s", firstBytes, secondBytes)
	}

	if _, err = installClawCode("/usr/local/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	afterUninstall, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var cleaned map[string]any
	if err := json.Unmarshal(afterUninstall, &cleaned); err != nil {
		t.Fatalf("parse settings after uninstall: %v", err)
	}
	if cleaned["model"] != "claude-sonnet-4-6" {
		t.Fatalf("lost model after uninstall: %#v", cleaned["model"])
	}
	cleanedServers, _ := cleaned["mcpServers"].(map[string]any)
	if cleanedServers["other"] == nil || cleanedServers["deja"] != nil {
		t.Fatalf("expected other kept and deja removed, got %#v", cleanedServers)
	}
	if _, err := os.Stat(skillPath); !os.IsNotExist(err) {
		t.Fatalf("expected skill file removed, stat err=%v", err)
	}
	if _, err := os.Stat(cmdPath); !os.IsNotExist(err) {
		t.Fatalf("expected command file removed, stat err=%v", err)
	}
}

func TestInstallClawCodeAutoRespectsEnvAndPreservesHooks(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(t.TempDir(), "custom-claw")
	t.Setenv("HOME", home)
	t.Setenv("CLAW_CONFIG_HOME", cfgDir)

	settingsPath := filepath.Join(cfgDir, "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{"type": "command", "command": "echo keep"}]
      }
    ]
  }
}
`
	if err := os.WriteFile(settingsPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := installClawCodeAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install auto: %v", err)
	}
	firstBytes, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(firstBytes)
	for _, want := range []string{"echo keep", "hook-tool", "hook-tool-after", `"PreToolUse"`, `"PostToolUse"`, `"PostToolUseFailure"`, `"mcpServers"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in settings.json, got:\n%s", want, text)
		}
	}
	if _, err = os.Stat(filepath.Join(home, ".claw", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("should use CLAW_CONFIG_HOME, not ~/.claw")
	}

	if _, err = installClawCodeAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("re-install auto: %v", err)
	}
	secondBytes, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("installClawCodeAuto not idempotent:\n--- first ---\n%s\n--- second ---\n%s", firstBytes, secondBytes)
	}

	if _, err = installClawCodeAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatalf("uninstall auto: %v", err)
	}
	afterBytes, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	afterText := string(afterBytes)
	if !strings.Contains(afterText, "echo keep") {
		t.Fatalf("expected unrelated hook preserved after uninstall, got:\n%s", afterText)
	}
	for _, unwanted := range []string{"hook-tool", "hook-tool-after", `"deja"`} {
		if strings.Contains(afterText, unwanted) {
			t.Fatalf("expected %q removed after uninstall, got:\n%s", unwanted, afterText)
		}
	}
}
