package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Gemini and Qwen both run a command before the agent loop, so auto-recall
// works there and not only MCP-on-demand. Their shapes differ in ways that
// only surfaced by running them, so the generated config is pinned here.
func TestGeminiAndQwenAutoHookShapes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GEMINI_CLI_HOME", "")
	t.Setenv("DEJA_QWEN_ROOT", "")

	for _, c := range []struct {
		name    string
		install func(string, bool) (installResult, error)
		event   string
		timeout float64
		matcher string
	}{
		// Gemini has no SessionStart; BeforeAgent is where it can set up
		// context. Its timeout is in MILLISECONDS — a Claude-style 10 is ten
		// milliseconds and the hook is killed before it can answer.
		{"gemini", installGeminiAuto, "BeforeAgent", 10000, ""},
		// Qwen forked from an older Gemini and kept SessionStart, a matcher
		// and seconds.
		{"qwen", installQwenAuto, "SessionStart", 10, "startup|resume"},
	} {
		t.Run(c.name, func(t *testing.T) {
			res, err := c.install("/usr/local/bin/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			read := func() map[string]any {
				b, err := os.ReadFile(res.Path)
				if err != nil {
					t.Fatal(err)
				}
				var root map[string]any
				if err := json.Unmarshal(b, &root); err != nil {
					t.Fatal(err)
				}
				return root
			}
			hooks, _ := read()["hooks"].(map[string]any)
			entries, _ := hooks[c.event].([]any)
			if len(entries) != 1 {
				t.Fatalf("%d entries under %s, want 1", len(entries), c.event)
			}
			entry, _ := entries[0].(map[string]any)
			if got, _ := entry["matcher"].(string); got != c.matcher {
				t.Errorf("matcher = %q, want %q", got, c.matcher)
			}
			inner, _ := entry["hooks"].([]any)
			if len(inner) != 1 {
				t.Fatalf("inner hooks = %d, want 1", len(inner))
			}
			h, _ := inner[0].(map[string]any)
			if h["command"] != "/usr/local/bin/deja hook-context" {
				t.Errorf("command = %v", h["command"])
			}
			if to, _ := h["timeout"].(float64); to != c.timeout {
				t.Errorf("timeout = %v, want %v (gemini counts milliseconds, qwen seconds)", h["timeout"], c.timeout)
			}
			if _, err := c.install("/usr/local/bin/deja", false); err != nil {
				t.Fatal(err)
			}
			hooks, _ = read()["hooks"].(map[string]any)
			if entries, _ = hooks[c.event].([]any); len(entries) != 1 {
				t.Fatalf("reinstall left %d entries", len(entries))
			}
			if _, err := c.install("/usr/local/bin/deja", true); err != nil {
				t.Fatal(err)
			}
			if _, still := read()["hooks"]; still {
				t.Error("uninstall left hooks behind")
			}
		})
	}
}

// The settings file belongs to the host: everything that is not ours must
// survive both an install and an uninstall untouched.
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

// Kimi keeps hooks in config.toml as an array of tables, so our entry cannot
// be removed by table name the way the MCP block is — several [[hooks]] are
// legal and only one is ours.
func TestKimiAutoHookAndItsRemoval(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("KIMI_CODE_HOME", "")
	t.Setenv("DEJA_KIMI_ROOT", "")

	res, err := installKimiAuto("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{"[[hooks]]", `event = "SessionStart"`, `command = "/usr/local/bin/deja hook-context"`, "timeout = 10"} {
		if !strings.Contains(got, want) {
			t.Errorf("config is missing %q:\n%s", want, got)
		}
	}
	if _, err := installKimiAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(res.Path)
	if n := strings.Count(string(b), "[[hooks]]"); n != 1 {
		t.Fatalf("reinstall produced %d hook entries", n)
	}
	if _, err := installKimiAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(res.Path)
	if strings.Contains(string(b), "hook-context") {
		t.Errorf("uninstall left our hook: %s", b)
	}
}

// A config the user also edits by hand must come back intact: their own
// hooks, their providers, their keys.
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
