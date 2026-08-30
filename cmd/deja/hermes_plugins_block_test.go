package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// hermesConfig is the reader's own file, before deja touches it.
func hermesConfig(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	path := filepath.Join(sources.HermesHome(), "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The MCP half of this file drops the block it created (#2604). The plugin
// half wrote `plugins:\n  enabled:\n    - deja\n` where no block existed and
// took back only the `- deja` line, so an empty block deja made survived every
// uninstall — and `enabled:` with nothing under it parses as null (#2672).
func TestUninstallDropsThePluginsBlockItCreated(t *testing.T) {
	before := "# hermes\nprofile: default\n"
	path := hermesConfig(t, before)
	if _, err := installTarget("hermes-auto", "/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "- deja") {
		t.Fatalf("install did not enable the plugin:\n%s", b)
	}
	if _, err := installTarget("hermes-auto", "/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Fatalf("the config did not come back as it was:\nwant:\n%s\ngot:\n%s", before, after)
	}
}

// A block the reader wrote stays, empty or not: deja takes back its own line
// and nothing else.
func TestUninstallKeepsAPluginsBlockTheReaderWrote(t *testing.T) {
	for _, before := range []string{
		"# hermes\nprofile: default\n\nplugins:\n  enabled:\n",
		"# hermes\nprofile: default\n\nplugins:\n  enabled:\n    - theirs\n",
	} {
		t.Run(strings.ReplaceAll(before, "\n", "·"), func(t *testing.T) {
			path := hermesConfig(t, before)
			if _, err := installTarget("hermes-auto", "/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			if _, err := installTarget("hermes-auto", "/bin/deja", true); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != before {
				t.Fatalf("a block the reader wrote was changed:\nwant:\n%s\ngot:\n%s", before, after)
			}
		})
	}
}
