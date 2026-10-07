package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readCodeBuddyJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return m
}

// `deja install codebuddy` writes the server where CodeBuddy's own
// resolveMcpFilePath looks first: <config>/.mcp.json, unless an older file
// already holds the user's servers.
func TestInstallCodeBuddyMCP(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	r, err := installTarget("codebuddy", "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".codebuddy", ".mcp.json")
	if r.Path != want {
		t.Fatalf("path = %q, want %q", r.Path, want)
	}
	servers, _ := readCodeBuddyJSON(t, want)["mcpServers"].(map[string]any)
	entry, _ := servers["deja"].(map[string]any)
	if command, _ := mcpCommandArgs("/bin/deja"); entry["command"] != command {
		t.Fatalf("mcpServers.deja = %v", entry)
	}

	// A legacy ~/.codebuddy.json is the file CodeBuddy reads when it is the
	// only one there, so the entry goes into it rather than a new file it
	// would shadow.
	if err := os.Remove(want); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(home, ".codebuddy.json")
	if err := os.WriteFile(legacy, []byte(`{"mcpServers":{"other":{"command":"x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if r, err = installTarget("codebuddy", "/bin/deja", false); err != nil || r.Path != legacy {
		t.Fatalf("legacy install = %q, %v", r.Path, err)
	}
	servers, _ = readCodeBuddyJSON(t, legacy)["mcpServers"].(map[string]any)
	if servers["deja"] == nil || servers["other"] == nil {
		t.Fatalf("legacy servers = %v", servers)
	}

	// CODEBUDDY_CONFIG_DIR moves the config home and the legacy file with it.
	cfg := filepath.Join(home, "profile")
	t.Setenv("CODEBUDDY_CONFIG_DIR", cfg)
	if r, err = installTarget("codebuddy", "/bin/deja", false); err != nil || r.Path != filepath.Join(cfg, ".mcp.json") {
		t.Fatalf("CODEBUDDY_CONFIG_DIR install = %q, %v", r.Path, err)
	}
}

// `deja install codebuddy-auto` wires the Claude Code hook block into
// ~/.codebuddy/settings.json and the server beside it; uninstall takes both
// out and leaves the user's own hooks.
func TestInstallCodeBuddyAuto(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	settings := filepath.Join(home, ".codebuddy", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	own := `{"model":"x","hooks":{"Stop":[{"hooks":[{"type":"command","command":"say done"}]}]}}`
	if err := os.WriteFile(settings, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("codebuddy-auto", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	root := readCodeBuddyJSON(t, settings)
	hooks, _ := root["hooks"].(map[string]any)
	for _, w := range codeBuddyHookWiring {
		entries, _ := hooks[w.Event].([]any)
		var entry map[string]any
		var h map[string]any
		for _, e := range entries {
			m := e.(map[string]any)
			hh := m["hooks"].([]any)[0].(map[string]any)
			if strings.HasSuffix(hh["command"].(string), " "+w.Sub) {
				if entry != nil {
					t.Fatalf("%s: %s wired twice", w.Event, w.Sub)
				}
				entry, h = m, hh
			}
		}
		if entry == nil {
			t.Fatalf("%s: no %s entry in %v", w.Event, w.Sub, entries)
		}
		if m, _ := entry["matcher"].(string); m != w.Matcher {
			t.Fatalf("%s matcher = %q, want %q", w.Event, m, w.Matcher)
		}
		if h["type"] != "command" {
			t.Fatalf("%s hook = %v", w.Event, h)
		}
		// Seconds: CodeBuddy multiplies the field by 1000.
		if h["timeout"] != float64(60) {
			t.Fatalf("%s timeout = %v, want 60 seconds", w.Event, h["timeout"])
		}
	}
	if root["model"] != "x" || hooks["Stop"] == nil {
		t.Fatalf("the user's settings were not kept: %v", root)
	}
	if _, err := os.Stat(filepath.Join(home, ".codebuddy", ".mcp.json")); err != nil {
		t.Fatalf("codebuddy-auto wrote no MCP entry: %v", err)
	}

	if _, err := installTarget("codebuddy-auto", "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	root = readCodeBuddyJSON(t, settings)
	hooks, _ = root["hooks"].(map[string]any)
	if len(hooks) != 1 || hooks["Stop"] == nil {
		t.Fatalf("after uninstall hooks = %v", hooks)
	}
}

func TestCodeBuddyIsAnInstallTarget(t *testing.T) {
	hermeticEnv(t)
	names := strings.Join(installTargetNames(), " ")
	for _, n := range []string{"codebuddy", "codebuddy-auto"} {
		if !strings.Contains(" "+names+" ", " "+n+" ") {
			t.Fatalf("installTargetNames lacks %s", n)
		}
	}
	if autoTargetFor("codebuddy") != "codebuddy-auto" {
		t.Fatalf("--auto maps codebuddy to %q", autoTargetFor("codebuddy"))
	}
}

// --auto finds CodeBuddy by the session store it writes for itself, not by
// the config directory deja creates when it installs.
func TestCodeBuddyDetectedByItsStore(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	has := func() bool {
		for _, n := range existingTargets() {
			if n == "codebuddy" {
				return true
			}
		}
		return false
	}
	if err := os.MkdirAll(filepath.Join(home, ".codebuddy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if has() {
		t.Fatal("an empty ~/.codebuddy counted as a CodeBuddy machine")
	}
	if err := os.MkdirAll(filepath.Join(home, ".codebuddy", "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !has() {
		t.Fatal("~/.codebuddy/projects did not count as a CodeBuddy machine")
	}
}
