package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Some editors never write a final newline. Every writer here ends with one,
// and nothing put back what the file came in with, so a config someone owned
// came back changed for nothing — #2606 the other way round (#2619).
func TestInstallLeavesAFileEndingAsItFoundIt(t *testing.T) {
	cases := []struct {
		target, rel, body string
	}{
		{"codex", ".codex/config.toml", "[tools]\nweb = true"},
		{"grok", ".grok/config.toml", "[tools]\nweb = true"},
		{"goose", ".config/goose/config.yaml", "GOOSE_MODEL: gpt-5"},
		{"hermes", ".hermes/config.yaml", "profile: default"},
		{"opencode", ".config/opencode/opencode.jsonc", "{\n  \"mcp\": {\n    \"theirs\": { \"type\": \"local\" }\n  }\n}"},
	}
	for _, tc := range cases {
		t.Run(tc.target, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			path := filepath.Join(home, filepath.FromSlash(tc.rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := installTarget(tc.target, "/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(string(b), "\n") {
				t.Fatalf("install added a final newline the reader had not written:\n%q", b)
			}
			if !strings.Contains(string(b), "deja") {
				t.Fatalf("install wrote nothing:\n%s", b)
			}
		})
	}
}

// A file deja creates still ends the way a text file should.
func TestInstallEndsAFileItCreatedWithANewline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if _, err := installTarget("codex", "/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(b), "\n") {
		t.Fatalf("a config deja wrote itself should end with a newline:\n%q", b)
	}
}
