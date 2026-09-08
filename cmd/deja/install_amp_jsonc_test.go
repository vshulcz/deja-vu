package main

import (
	"os"
	"path/filepath"
	"testing"
)

// An editor's JSONC carries trailing commas as well as comments; the amp
// reader stripped comments and then demanded strict JSON (#3236).
func TestInstallAmpTakesAFileWithATrailingComma(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, "settings.json")
	t.Setenv("AMP_SETTINGS_FILE", path)
	before := "{\n  // mine\n  \"amp.mcpServers\": {\"context7\": {\"command\": \"npx\", \"args\": [\"-y\"],},},\n  \"amp.notifications.enabled\": true,\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installAmpMCP("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install refused the editor's own file: %v", err)
	}
	root := ampSettings(t, path)
	servers, _ := root[ampServersKey].(map[string]any)
	if servers["context7"] == nil || servers["deja"] == nil {
		t.Fatalf("install did not sit beside the existing server: %v", root)
	}
}
