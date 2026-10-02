package main

import (
	"os"
	"path/filepath"
	"strings"
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

// Amp's settings file is JSONC, and re-marshalling it dropped the reader's
// comments and sorted the keys, with no way back on uninstall (#4357). The
// block key is one literal with a dot in it, not a path.
func TestInstallAmpKeepsCommentsAndOrderOnARoundTrip(t *testing.T) {
	for name, before := range map[string]string{
		"block":    "// my amp settings\n{\n  \"amp.notifications.enabled\": false, // quiet\n  \"amp.mcpServers\": {\n    \"playwright\": { \"command\": \"npx\", \"args\": [\"-y\", \"@playwright/mcp@latest\"] },\n  },\n  \"amp.dangerouslyAllowAll\": true,\n}\n",
		"no block": "// my amp settings\n{\n  \"amp.notifications.enabled\": false, // quiet\n  \"amp.dangerouslyAllowAll\": true,\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
			path := filepath.Join(home, "settings.json")
			t.Setenv("AMP_SETTINGS_FILE", path)
			if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := installAmpMCP("/usr/local/bin/deja", false); err != nil {
				t.Fatal(err)
			}
			installed, _ := os.ReadFile(path)
			for _, want := range []string{"// my amp settings", "// quiet"} {
				if !strings.Contains(string(installed), want) {
					t.Fatalf("install dropped %q:\n%s", want, installed)
				}
			}
			root := ampSettings(t, path)
			servers, _ := root[ampServersKey].(map[string]any)
			if servers["deja"] == nil || root["amp"] != nil {
				t.Fatalf("deja is not under the literal %q key:\n%s", ampServersKey, installed)
			}
			if _, err := installAmpMCP("/usr/local/bin/deja", true); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(path)
			if string(after) != before {
				t.Fatalf("round trip changed the file:\n--- before\n%s--- after\n%s", before, after)
			}
		})
	}
}
