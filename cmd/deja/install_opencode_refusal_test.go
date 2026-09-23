package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A config deja declines to edit costs the config, not the plugin. The two are
// independent — the plugin shells out to the hook launcher and reads nothing
// from the config — but the refusal used to end the whole target, so a machine
// whose servers sit under `mcp.servers` was left with no auto-recall at all and
// no sign that half the install had been skipped (#3937).
func TestOpencodeAutoStillWritesThePluginWhenTheConfigIsRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_OPENCODE_MAJOR", "2")
	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	nested := "{\n  // mine\n  \"mcp\": {\n    \"servers\": {\n      \"deja\": {\n        \"type\": \"local\",\n" +
		"        \"command\": [\"/usr/local/bin/deja\", \"mcp\"],\n        \"environment\": { \"DEJA_HOME\": \"/mnt/nas/deja\" }\n" +
		"      }\n    }\n  }\n}\n"
	cfg := filepath.Join(dir, "opencode.jsonc")
	if err := os.WriteFile(cfg, []byte(nested), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := installOpencodeAuto("/usr/local/bin/deja", false)
	if err == nil {
		t.Fatal("the nested config was edited instead of refused")
	}
	if !strings.Contains(err.Error(), "mcp.servers") {
		t.Errorf("the refusal no longer says which shape it found: %v", err)
	}
	// The config is the reader's, untouched.
	if b, readErr := os.ReadFile(cfg); readErr != nil || string(b) != nested {
		t.Errorf("the refused config was written to: %v %q", readErr, string(b))
	}
	// The plugin is deja's, and it is the half that still works.
	b, err := os.ReadFile(filepath.Join(dir, "plugins", "deja.js"))
	if err != nil {
		t.Fatalf("the plugin was skipped along with the config: %v", err)
	}
	if !strings.Contains(string(b), "hook-context") {
		t.Error("the plugin was written without the digest hook")
	}
}
