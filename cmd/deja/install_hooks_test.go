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
