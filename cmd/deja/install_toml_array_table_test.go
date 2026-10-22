package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A config that spells deja's entry as an array of tables, `[[mcp_servers.deja]]`,
// got a plain `[mcp_servers.deja]` appended: tomlInlineKey trimmed the brackets
// off every header, so it read the array as the table deja was about to write,
// and tomlMCPHeader skips `[[` headers, so no deja block was found. TOML forbids
// declaring a key as both, so the file stopped loading in every TOML parser
// (#5297). Refused now with the file untouched, like the inline spellings.
func TestInstallRefusesAnArrayOfTablesNamedLikeDejasTable(t *testing.T) {
	for _, c := range []struct {
		target string
		path   func() string
	}{
		{"codex", func() string { return filepath.Join(sources.CodexHome(), "config.toml") }},
		{"grok", func() string { return filepath.Join(sources.GrokHome(), "config.toml") }},
	} {
		t.Run(c.target, func(t *testing.T) {
			hermeticEnv(t)
			p := c.path()
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			seed := "[[mcp_servers.deja]]\ncommand = \"/usr/local/bin/deja\"\nargs = [\"mcp\"]\n"
			if err := os.WriteFile(p, []byte(seed), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := captureRun(t, "install", c.target, "--no-index", "--no-guidance")
			if err == nil || !strings.Contains(err.Error(), "array of tables") {
				t.Errorf("install did not refuse the array of tables: %v", err)
			}
			if got, _ := os.ReadFile(p); string(got) != seed {
				t.Errorf("config changed:\n%s", got)
			}
		})
	}
}

// The control: deja's own `[[hooks]]` header is an array of tables by design, so
// a reader's `[[hooks]]` entries stay legal beside the one it appends.
func TestInstallKeepsAReadersHooksArrayOfTables(t *testing.T) {
	hermeticEnv(t)
	p := filepath.Join(sources.KimiConfigDir(), "config.toml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := "default_model = \"kimi-k2\"\n\n[[hooks]]\nevent = \"Stop\"\ncommand = \"my-hook\"\n"
	if err := os.WriteFile(p, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "kimi-auto", "--no-index", "--no-guidance"); err != nil {
		t.Fatalf("install refused a file whose [[hooks]] entries are the reader's own: %v", err)
	}
	if got, _ := os.ReadFile(p); !strings.Contains(string(got), "my-hook") {
		t.Errorf("the reader's hook was lost:\n%s", got)
	}
}
