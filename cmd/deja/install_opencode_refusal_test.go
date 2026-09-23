package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A 2.x config keeps MCP servers under `mcp.servers`. The auto target must wire
// that object and still write the plugin used for recall (#3938).
func TestOpencodeAutoUpdatesNestedConfigAndWritesThePlugin(t *testing.T) {
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

	if _, err := installOpencodeAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("nested config install failed: %v", err)
	}
	// The config stays in the shape the OpenCode 2.x reader expects.
	b, readErr := os.ReadFile(cfg)
	if readErr != nil {
		t.Fatal(readErr)
	}
	got := jsoncValue(t, b)
	mcp, ok := got["mcp"].(map[string]any)
	if !ok {
		t.Fatalf("mcp block is missing: %s", b)
	}
	servers, ok := mcp["servers"].(map[string]any)
	if !ok {
		t.Fatalf("servers block is missing: %s", b)
	}
	if _, ok := servers["deja"]; !ok {
		t.Fatalf("deja is not under mcp.servers: %s", b)
	}
	if _, ok := mcp["deja"]; ok {
		t.Fatalf("deja was written beside mcp.servers: %s", b)
	}
	if strings.Contains(string(b), "DEJA_HOME") == false {
		t.Error("the existing environment was not preserved")
	}
	// The plugin is deja's, and it is the half that still works.
	b, err := os.ReadFile(filepath.Join(dir, "plugins", "deja.js"))
	if err != nil {
		t.Fatalf("the plugin was not written: %v", err)
	}
	if !strings.Contains(string(b), "hook-context") {
		t.Error("the plugin was written without the digest hook")
	}
}
