package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func cursorHooksFile(t *testing.T, home string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".cursor", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("hooks.json is not valid JSON: %v", err)
	}
	return root
}

func cursorCommands(t *testing.T, root map[string]any, event string) []string {
	t.Helper()
	hooks, _ := root["hooks"].(map[string]any)
	entries, _ := hooks[event].([]any)
	var out []string
	for _, e := range entries {
		m, _ := e.(map[string]any)
		if c, ok := m["command"].(string); ok {
			out = append(out, c)
		}
	}
	return out
}

func TestInstallCursorHooksWritesSessionStart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CURSOR_CONFIG_DIR", filepath.Join(home, ".cursor"))
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installCursorHooks("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	root := cursorHooksFile(t, home)
	// Cursor's own event names, not Claude's — the file is read by cursor.
	if got := cursorCommands(t, root, "sessionStart"); len(got) != 1 || got[0] != "/bin/deja hook-context" {
		t.Fatalf("sessionStart = %v", got)
	}
	if got := cursorCommands(t, root, "beforeSubmitPrompt"); len(got) != 1 {
		t.Fatalf("beforeSubmitPrompt = %v", got)
	}
	if root["version"] != float64(1) {
		t.Fatalf("version = %v, want 1", root["version"])
	}
}

func TestInstallCursorHooksIsIdempotentAndReversible(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CURSOR_CONFIG_DIR", filepath.Join(home, ".cursor"))
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A hook the user already had; uninstall must not touch it.
	seed := `{"version":1,"hooks":{"sessionStart":[{"command":"/usr/bin/mine"}]}}`
	path := filepath.Join(home, ".cursor", "hooks.json")
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := installCursorHooks("/bin/deja", false); err != nil {
			t.Fatalf("install %d: %v", i, err)
		}
	}
	if got := cursorCommands(t, cursorHooksFile(t, home), "sessionStart"); len(got) != 2 {
		t.Fatalf("repeated installs duplicated entries: %v", got)
	}
	if _, err := installCursorHooks("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	root := cursorHooksFile(t, home)
	got := cursorCommands(t, root, "sessionStart")
	if len(got) != 1 || got[0] != "/usr/bin/mine" {
		t.Fatalf("uninstall did not leave the user's hook alone: %v", got)
	}
	if c := cursorCommands(t, root, "beforeSubmitPrompt"); len(c) != 0 {
		t.Fatalf("beforeSubmitPrompt survived uninstall: %v", c)
	}
}
