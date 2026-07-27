package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsHookLeavesTheRestOfTheFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	original := `{"mcpServers":{"other":{"command":"x"}},"selectedAuthType":"oauth","hooks":{"AfterTool":[{"hooks":[{"type":"command","command":"someone-else"}]}]}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	read := func() map[string]any {
		b, _ := os.ReadFile(path)
		var root map[string]any
		_ = json.Unmarshal(b, &root)
		return root
	}
	if _, err := installSettingsHook(path, "BeforeAgent", "", 10000, "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	root := read()
	if _, ok := root["mcpServers"]; !ok {
		t.Error("mcpServers was dropped")
	}
	if root["selectedAuthType"] != "oauth" {
		t.Error("auth setting was dropped")
	}
	hooks, _ := root["hooks"].(map[string]any)
	if _, ok := hooks["AfterTool"]; !ok {
		t.Error("another tool's hook was dropped")
	}
	if _, err := installSettingsHook(path, "BeforeAgent", "", 10000, "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	hooks, _ = read()["hooks"].(map[string]any)
	if _, ok := hooks["AfterTool"]; !ok {
		t.Error("uninstall removed a hook that was not ours")
	}
	if _, ok := hooks["BeforeAgent"]; ok {
		t.Error("uninstall left our hook")
	}
}
func TestKimiUninstallKeepsSomeoneElsesHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("KIMI_CODE_HOME", "")
	t.Setenv("DEJA_KIMI_ROOT", "")

	dir := filepath.Join(home, ".kimi-code")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	original := "[providers.openrouter]\napi_key = \"secret\"\n\n[[hooks]]\nevent = \"Stop\"\ncommand = \"mine\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installKimiAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installKimiAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	got := string(b)
	for _, want := range []string{"[providers.openrouter]", `api_key = "secret"`, `event = "Stop"`, `command = "mine"`} {
		if !strings.Contains(got, want) {
			t.Errorf("uninstall destroyed %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "hook-context") {
		t.Error("uninstall left our entry behind")
	}
}

// An install from a newer deja has to update the entry it already owns —
// otherwise anyone who installed once never sees a field added later, and a
// moved binary leaves a second entry firing beside the new one.
func TestCodexHookInstallAdoptsAnOlderEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// What an older version wrote: our command, no status message.
	seed := `{"hooks":{"SessionStart":[{"matcher":"startup|resume","hooks":[{"type":"command","command":"/opt/old/deja hook-context","timeout":10}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command       string `json:"command"`
				StatusMessage string `json:"statusMessage"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	groups := root.Hooks["SessionStart"]
	var ours []string
	for _, g := range groups {
		for _, h := range g.Hooks {
			if !strings.Contains(h.Command, "deja") {
				continue
			}
			ours = append(ours, h.Command)
			if h.StatusMessage == "" {
				t.Error("adopted entry never gained the status message")
			}
		}
	}
	if len(ours) != 1 {
		t.Fatalf("%d deja hooks, want 1: %v", len(ours), ours)
	}
	if !strings.HasPrefix(ours[0], "/usr/local/bin/deja") {
		t.Fatalf("kept the stale path: %s", ours[0])
	}
}

// Kimi took three wrong readings: it injects the hook's plain stdout rather
// than a JSON field, only UserPromptSubmit does it, and its prompt arrives as
// content parts. Each one on its own looks like "recall found nothing".
func TestKimiHooksUserPromptSubmitWithPlainOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installKimiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".kimi-code", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(b)
	if strings.Contains(cfg, `event = "SessionStart"`) {
		t.Fatal("SessionStart runs but its output goes nowhere: wrong event")
	}
	if !strings.Contains(cfg, `event = "UserPromptSubmit"`) {
		t.Fatalf("no UserPromptSubmit hook:\n%s", cfg)
	}
	// Kimi reads stdout verbatim; the JSON envelope would land in the prompt.
	if !strings.Contains(cfg, "hook-prompt --plain") {
		t.Fatalf("hook does not ask for the bare digest:\n%s", cfg)
	}
	if _, err := installKimiAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	b, _ = os.ReadFile(filepath.Join(home, ".kimi-code", "config.toml"))
	if strings.Contains(string(b), "hook-prompt") {
		t.Fatalf("uninstall left the hook behind:\n%s", b)
	}
}
