package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A config deja cannot parse is refused and left alone. A config it can parse
// whose MCP block is a list was rewritten instead, and the servers in it went
// out with the write (#2399).
func TestInstallRefusesABlockOfTheWrongShape(t *testing.T) {
	cases := []struct {
		name   string
		target string
		rel    string
		before string
		keep   []string
	}{
		{"claude, mcpServers as a list", "claude", ".claude.json",
			`{"theme":"dark","mcpServers":[{"name":"mine","command":"/usr/bin/mine"}]}`,
			[]string{"/usr/bin/mine", `"theme"`}},
		{"opencode, mcp as a list", "opencode", ".config/opencode/opencode.json",
			`{"model":"theirs/model","mcp":["mine"]}`,
			[]string{`"mine"`, "theirs/model"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))
			path := filepath.Join(home, filepath.FromSlash(tc.rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.before), 0o644); err != nil {
				t.Fatal(err)
			}

			_, err := installTarget(tc.target, "/usr/local/bin/deja", false)
			if err == nil {
				t.Fatalf("install rewrote a block it did not understand")
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("the refusal does not name the file: %v", err)
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, k := range tc.keep {
				if !strings.Contains(string(body), k) {
					t.Errorf("%s went out with the write:\n%s", k, body)
				}
			}
		})
	}
}
