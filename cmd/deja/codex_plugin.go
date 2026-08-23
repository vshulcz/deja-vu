package main

import (
	"os"
	"path/filepath"
	"regexp"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// codexPluginEntry matches the config line `codex plugin add` writes:
// [plugins."deja-vu@<marketplace>"], enabled on the next line.
var codexPluginEntry = regexp.MustCompile(`(?m)^\[plugins\."deja-vu@[^"]+"\]\s*\n(?:[^\[]*\n)?\s*enabled\s*=\s*true`)

// codexPluginInstalled reports whether the Codex plugin from codex-plugin/ is
// installed and enabled. It carries the same MCP server and the same hooks the
// installer writes, and stands down where the installer already wired them —
// so a machine with only the plugin is wired, and doctor calling it "not
// wired" would send someone to run an install they do not need.
func codexPluginInstalled() bool {
	b, err := os.ReadFile(filepath.Join(sources.CodexHome(), "config.toml"))
	if err != nil {
		return false
	}
	return codexPluginEntry.Match(b)
}
