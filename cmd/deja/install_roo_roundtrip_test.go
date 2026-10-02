package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Roo writes mcp_settings.json on first start with an empty, multi-line
// mcpServers object. Install and uninstall gave it back as `{}`: the emptied
// object was re-marshalled, though the bytes it had were in the .bak beside it
// (#4423).
func TestUninstallRooGivesBackTheDefaultSettingsBytes(t *testing.T) {
	hermeticEnv(t)
	const original = "{\n  \"mcpServers\": {\n\n  }\n}"
	root := rooHost(t, t.TempDir(), original)
	t.Setenv("DEJA_ROO_ROOTS", root)
	path := filepath.Join(root, "settings", "mcp_settings.json")
	if _, err := captureRun(t, "install", "roo", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "roo"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != original {
		t.Errorf("settings after uninstall = %q, want the bytes Roo wrote %q", b, original)
	}

	// The control: a server added while deja was wired is not in the
	// snapshot, so the snapshot is not what the file goes back to.
	if _, err := captureRun(t, "install", "roo", "--no-index"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	mine := strings.Replace(string(b), "\"mcpServers\": {", "\"mcpServers\": {\n    \"fs\": {\"command\": \"fs-mcp\"},", 1)
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "roo"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "fs-mcp") || strings.Contains(string(b), "\"deja\"") {
		t.Errorf("settings after the second uninstall:\n%s", b)
	}
}

// The snapshot stands in only for what deja took out. A config deja is not in
// is the reader's as it is now, and an uninstall that meets one must not put
// back the layout the snapshot had, nor a number the snapshot rounds to the
// same float.
func TestUninstallLeavesAJSONConfigWithoutDejaAsItIs(t *testing.T) {
	t.Cleanup(func() { removingWiring = false })
	removingWiring = true
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path+".bak", []byte("{\n  \"mcpServers\": {}\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mine := []byte(`{"mcpServers":{}}` + "\n")
	if err := os.WriteFile(path, mine, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeIfChanged(path, mine, mine); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != string(mine) {
		t.Errorf("a config with no deja in it became %q, want it left as %q", b, mine)
	}

	// A number the snapshot holds differently, past float64's precision.
	if err := os.WriteFile(path+".bak", []byte("{\"id\": 12345678901234567890}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := []byte("{\"id\": 12345678901234567891, \"mcpServers\": {\"deja\": {}}}\n")
	next := []byte("{\"id\": 12345678901234567891}\n")
	if err := os.WriteFile(path, old, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeIfChanged(path, old, next); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != string(next) {
		t.Errorf("uninstall wrote %q, want %q: the snapshot's id is another number", b, next)
	}
}
