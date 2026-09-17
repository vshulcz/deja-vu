package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ZCode's writers created the containers they needed and never recorded them,
// so an uninstall left `{"hooks":{"enabled":true},"mcp":{"servers":{}}}` in a
// file deja had created itself — the shape #2604 exists to prevent (#3690).
func TestUninstallZCodeLeavesNoContainerItAdded(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	path := filepath.Join(home, ".zcode", "cli", "config.json")
	for _, target := range []string{"zcode", "zcode-auto"} {
		if _, err := captureRun(t, "install", target, "--no-index"); err != nil {
			t.Fatalf("install %s: %v", target, err)
		}
	}
	for _, target := range []string{"zcode", "zcode-auto"} {
		if _, err := captureRun(t, "uninstall", target); err != nil {
			t.Fatalf("uninstall %s: %v", target, err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		// Removed outright is the better answer and the one the config
		// writers give for a file that was entirely deja's.
		return
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	for _, key := range []string{"mcp", "hooks"} {
		if _, ok := root[key]; ok {
			t.Errorf("uninstall left the %q block deja added: %s", key, b)
		}
	}
}

// A container the reader already had is theirs, empty or not — including the
// `enabled` switch, which is gemini's rule: something else may be running on it.
func TestUninstallZCodeKeepsWhatTheReaderHad(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	path := filepath.Join(home, ".zcode", "cli", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	theirs := map[string]any{
		"hooks": map[string]any{"enabled": true},
		"mcp":   map[string]any{"servers": map[string]any{"theirs": map[string]any{"command": "their-server"}}},
	}
	b, err := json.Marshal(theirs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"zcode", "zcode-auto"} {
		if _, err := captureRun(t, "install", target, "--no-index"); err != nil {
			t.Fatalf("install %s: %v", target, err)
		}
		if _, err := captureRun(t, "uninstall", target); err != nil {
			t.Fatalf("uninstall %s: %v", target, err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("uninstall removed a config the reader had: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(got, &root); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	hooks, _ := root["hooks"].(map[string]any)
	if on, _ := hooks["enabled"].(bool); !on {
		t.Errorf("uninstall turned off a switch that was not deja's: %s", got)
	}
	mcp, _ := root["mcp"].(map[string]any)
	servers, _ := mcp["servers"].(map[string]any)
	if _, ok := servers["theirs"]; !ok {
		t.Errorf("uninstall took someone else's server with it: %s", got)
	}
}
