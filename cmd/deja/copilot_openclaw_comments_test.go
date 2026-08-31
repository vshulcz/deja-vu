package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The two targets #1664 did not reach: both write their entry through the
// parsed path only, so a `//` line in the config refused the target outright
// and the reader who annotated it could not install deja at all (#2783).
func TestACommentedConfigStillTakesTheEntry(t *testing.T) {
	for _, c := range []struct {
		target string
		write  func(exe string, uninstall bool) (installResult, error)
		path   func() string
		before string
		// block is where the entry ends up, read back from the parsed file.
		block []string
	}{{
		target: "copilot",
		write:  installCopilotMCP,
		path:   func() string { return filepath.Join(sources.Home(), ".copilot", "mcp-config.json") },
		before: "{\n  // the ordering here is deliberate\n  \"mcpServers\": {}\n}\n",
		block:  []string{"mcpServers"},
	}, {
		target: "openclaw",
		write:  installOpenClawMCP,
		path:   func() string { return filepath.Join(sources.OpenClawStateDir(), "openclaw.json") },
		before: "{\n  // the ordering here is deliberate\n  \"mcp\": {\n    \"servers\": {}\n  }\n}\n",
		block:  []string{"mcp", "servers"},
	}} {
		t.Run(c.target, func(t *testing.T) {
			hermeticEnv(t)
			path := c.path()
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(c.before), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := c.write("/bin/deja", false); err != nil {
				t.Fatalf("a commented config was refused: %v", err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), "// the ordering here is deliberate") {
				t.Errorf("the comment was dropped:\n%s", b)
			}
			if !strings.Contains(string(b), `"deja"`) {
				t.Fatalf("the entry was not written:\n%s", b)
			}
			var root map[string]any
			if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
				t.Fatalf("the file no longer parses: %v\n%s", err, b)
			}
			m := root
			for _, key := range c.block {
				next, ok := m[key].(map[string]any)
				if !ok {
					t.Fatalf("%q is not where the entry went:\n%s", key, b)
				}
				m = next
			}
			if _, ok := m["deja"].(map[string]any); !ok {
				t.Errorf("the entry is not under %v:\n%s", c.block, b)
			}

			if _, err := c.write("/bin/deja", true); err != nil {
				t.Fatalf("uninstall refused the commented config: %v", err)
			}
			b, err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), `"deja"`) {
				t.Errorf("the entry stayed behind:\n%s", b)
			}
			if !strings.Contains(string(b), "// the ordering here is deliberate") {
				t.Errorf("the comment was dropped on the way out:\n%s", b)
			}
		})
	}
}

// And when the block is not there at all: openclaw keeps its servers two keys
// deep, so a config with neither key needs both written, nested, without
// disturbing what the reader wrote around them.
func TestANestedBlockIsCreatedInACommentedConfig(t *testing.T) {
	for _, before := range []string{
		"{\n  // no mcp at all\n  \"theme\": \"dark\"\n}\n",
		"{\n  // an mcp block with no servers in it\n  \"mcp\": {}\n}\n",
	} {
		t.Run("", func(t *testing.T) {
			hermeticEnv(t)
			path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := installOpenClawMCP("/bin/deja", false); err != nil {
				t.Fatalf("a commented config was refused: %v", err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
				t.Fatalf("the file no longer parses: %v\n%s", err, b)
			}
			mcp, ok := root["mcp"].(map[string]any)
			if !ok {
				t.Fatalf("no mcp block:\n%s", b)
			}
			servers, ok := mcp["servers"].(map[string]any)
			if !ok {
				t.Fatalf("no servers block:\n%s", b)
			}
			if _, ok := servers["deja"].(map[string]any); !ok {
				t.Errorf("the entry is not under mcp.servers:\n%s", b)
			}
			if !strings.Contains(string(b), "//") {
				t.Errorf("the comment was dropped:\n%s", b)
			}
		})
	}
}
