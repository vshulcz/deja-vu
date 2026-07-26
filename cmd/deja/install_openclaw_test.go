package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openclawConfig(t *testing.T, home string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".openclaw", "openclaw.json"))
	if err != nil {
		t.Fatalf("read openclaw.json: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("openclaw.json is not valid JSON: %v", err)
	}
	return root
}

func openclawHookEntry(root map[string]any) (enabled bool, masterSwitch any, present bool) {
	hooks, _ := root["hooks"].(map[string]any)
	internal, _ := hooks["internal"].(map[string]any)
	entries, _ := internal["entries"].(map[string]any)
	e, ok := entries[openclawHookName].(map[string]any)
	if !ok {
		return false, internal["enabled"], false
	}
	on, _ := e["enabled"].(bool)
	return on, internal["enabled"], true
}

func TestInstallOpenClawHooksWritesPackAndEnablesIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installOpenClawHooks("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	dir := filepath.Join(home, ".openclaw", "hooks", openclawHookName)
	doc, err := os.ReadFile(filepath.Join(dir, "HOOK.md"))
	if err != nil {
		t.Fatalf("HOOK.md missing: %v", err)
	}
	// The frontmatter is what binds the pack to the event; without it the
	// handler is never called.
	if !strings.Contains(string(doc), `"events": ["agent:bootstrap"]`) {
		t.Fatalf("HOOK.md does not subscribe to agent:bootstrap:\n%s", doc)
	}
	handler, err := os.ReadFile(filepath.Join(dir, "handler.js"))
	if err != nil {
		t.Fatalf("handler.js missing: %v", err)
	}
	for _, want := range []string{"bootstrapFiles", "hook-context", `"/bin/deja"`} {
		if !strings.Contains(string(handler), want) {
			t.Fatalf("handler.js missing %q:\n%s", want, handler)
		}
	}
	// A pack that is not enabled in config is listed as ready and never runs.
	on, master, present := openclawHookEntry(openclawConfig(t, home))
	if !present || !on {
		t.Fatalf("hook entry not enabled: present=%v on=%v", present, on)
	}
	if master != true {
		t.Fatalf("hooks.internal.enabled = %v, want true", master)
	}
}

func TestInstallOpenClawHooksUninstallLeavesOtherHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgDir := filepath.Join(home, ".openclaw")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"mcp":{"servers":{}},"hooks":{"internal":{"enabled":true,"entries":{"boot-md":{"enabled":true}}}}}`
	if err := os.WriteFile(filepath.Join(cfgDir, "openclaw.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installOpenClawHooks("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := installOpenClawHooks("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "hooks", openclawHookName)); !os.IsNotExist(err) {
		t.Fatalf("pack survived uninstall: %v", err)
	}
	root := openclawConfig(t, home)
	if _, _, present := openclawHookEntry(root); present {
		t.Fatal("our entry survived uninstall")
	}
	hooks, _ := root["hooks"].(map[string]any)
	internal, _ := hooks["internal"].(map[string]any)
	entries, _ := internal["entries"].(map[string]any)
	if _, ok := entries["boot-md"]; !ok {
		t.Fatalf("uninstall removed the user's own hook entry: %v", entries)
	}
	if internal["enabled"] != true {
		t.Fatalf("uninstall flipped the master switch off with hooks still configured: %v", internal)
	}
	if _, ok := root["mcp"]; !ok {
		t.Fatal("uninstall dropped unrelated config")
	}
}

func TestInstallOpenClawHooksUninstallClearsConfigItCreated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installOpenClawHooks("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := installOpenClawHooks("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, ok := openclawConfig(t, home)["hooks"]; ok {
		t.Fatal("uninstall left the hooks block it created behind")
	}
}
