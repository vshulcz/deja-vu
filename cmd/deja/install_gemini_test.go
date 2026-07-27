package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func geminiExtensionHooks(t *testing.T, home string) map[string][]map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".gemini", "extensions", "deja", "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("extension hooks missing: %v", err)
	}
	var root struct {
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("hooks.json: %v", err)
	}
	// Gemini rejects the file outright when hooks is not an object.
	if root.Hooks == nil {
		t.Fatalf("hooks is not an object: %s", b)
	}
	return root.Hooks
}

func TestInstallGeminiWritesAnExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installGeminiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	hooks := geminiExtensionHooks(t, home)
	group := hooks["SessionStart"]
	if len(group) == 0 {
		t.Fatalf("no SessionStart group: %v", hooks)
	}
	inner, _ := group[0]["hooks"].([]any)
	if len(inner) != 1 {
		t.Fatalf("hooks = %v", inner)
	}
	h, _ := inner[0].(map[string]any)
	if cmd, _ := h["command"].(string); !strings.HasPrefix(cmd, "/bin/deja") {
		t.Fatalf("command = %v", h["command"])
	}
	// Gemini reads timeout in milliseconds — a Claude-style 10 kills the hook.
	if to, _ := h["timeout"].(float64); to < 1000 {
		t.Fatalf("timeout = %v, too small for milliseconds", h["timeout"])
	}
	// The manifest is what makes the directory an extension.
	if _, err := os.Stat(filepath.Join(home, ".gemini", "extensions", "deja", "gemini-extension.json")); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	// Loaded extensions still do nothing until the master switch is on.
	b, err := os.ReadFile(filepath.Join(home, ".gemini", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(b, &settings); err != nil {
		t.Fatal(err)
	}
	cfg, _ := settings["hooksConfig"].(map[string]any)
	if enabled, _ := cfg["enabled"].(bool); !enabled {
		t.Fatalf("hooksConfig = %v, hooks stay dormant without it", settings["hooksConfig"])
	}
	// The settings.json hook older versions wrote never fired; installing must
	// clear it rather than leave a dead integration looking installed.
	if hooks, ok := settings["hooks"]; ok && hooks != nil {
		t.Fatalf("stale settings.json hook left behind: %v", hooks)
	}
}

func TestInstallGeminiUninstallLeavesTheSwitchAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installGeminiAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := installGeminiAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "extensions", "deja")); !os.IsNotExist(err) {
		t.Fatalf("extension survived uninstall: %v", err)
	}
	// Other extensions may depend on hooksConfig, so it is not ours to switch off.
	b, _ := os.ReadFile(filepath.Join(home, ".gemini", "settings.json"))
	var settings map[string]any
	_ = json.Unmarshal(b, &settings)
	cfg, _ := settings["hooksConfig"].(map[string]any)
	if enabled, _ := cfg["enabled"].(bool); !enabled {
		t.Fatal("uninstall turned hooks off for every other extension too")
	}
}

// Kimi's hooks cannot carry context, so its installer exists only to clear
// what older versions wrote.
func TestKimiInstallsNoHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installKimiAuto("/bin/deja", false); err != nil {
		t.Fatalf("kimi: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(home, ".kimi-code", "config.toml")); err == nil {
		if strings.Contains(string(b), "hook-context") {
			t.Fatalf("kimi config gained a hook whose output is discarded: %s", b)
		}
	}
}

// Qwen was written off as having no hooks twice over: its timeout is in
// milliseconds, so the 10 deja used to write killed the hook instantly, and
// only UserPromptSubmit consumes additionalContext — a SessionStart-shaped
// reply is dropped because the event name does not match.
func TestQwenHooksUserPromptSubmitInMilliseconds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_QWEN_ROOT", "")
	if _, err := installQwenAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".qwen", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string  `json:"command"`
				Timeout float64 `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	if _, ok := root.Hooks["SessionStart"]; ok {
		t.Fatal("SessionStart fires but never takes the context: wrong event")
	}
	groups := root.Hooks["UserPromptSubmit"]
	if len(groups) == 0 || len(groups[0].Hooks) == 0 {
		t.Fatalf("no UserPromptSubmit hook: %s", b)
	}
	h := groups[0].Hooks[0]
	if !strings.HasSuffix(h.Command, "hook-prompt") {
		t.Fatalf("command = %q, want the prompt hook whose reply names UserPromptSubmit", h.Command)
	}
	if h.Timeout < 1000 {
		t.Fatalf("timeout = %v, too small for milliseconds", h.Timeout)
	}
}
