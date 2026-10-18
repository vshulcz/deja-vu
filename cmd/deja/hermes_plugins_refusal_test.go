package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A plugins.enabled deja refuses to edit was found only after the MCP entry
// and ~/.hermes/plugins/deja were written: the target reported refused while
// Hermes had a new server and a plugin it listed as "not enabled". The
// refusal now comes first and leaves the config and the plugins directory as
// they were.
func TestInstallHermesAutoRefusesBeforeWritingAnything(t *testing.T) {
	for name, before := range map[string]string{
		"empty flow mapping":   "model:\n  default: anthropic/claude-x\nplugins: {}\n",
		"multi-line flow list": "model:\n  default: anthropic/claude-x\nplugins:\n  enabled: [spotify,\n    other]\n",
		"scalar under enabled": "plugins:\n  enabled: spotify\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := hermesConfig(t, before)
			_, err := installTarget("hermes-auto", "/bin/deja", false)
			if err == nil || !strings.Contains(err.Error(), "plugins.enabled is not written in a shape deja can edit safely") {
				t.Fatalf("install did not refuse the plugins block: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != before {
				t.Fatalf("a refused install changed the config:\nwant:\n%s\ngot:\n%s", before, after)
			}
			if _, err := os.Stat(filepath.Join(sources.HermesHome(), "plugins")); !os.IsNotExist(err) {
				t.Fatalf("a refused install left a plugins directory behind: %v", err)
			}
		})
	}
}

// A plugin the reader switched off is not listed again, so its list is not
// looked at: the install goes ahead whatever shape plugins.enabled is in.
func TestInstallHermesAutoSkipsTheShapeCheckWhenDisabled(t *testing.T) {
	path := hermesConfig(t, "plugins:\n  enabled: spotify\n  disabled: [deja]\n")
	if _, err := installTarget("hermes-auto", "/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "mcp_servers:") {
		t.Fatalf("install did not write the MCP entry:\n%s", b)
	}
}
