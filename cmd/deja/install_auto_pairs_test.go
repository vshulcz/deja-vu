package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `deja install --auto` maps a detected harness to one target, so that target
// has to carry everything that harness can take. gemini's did not: it wrote the
// hooks extension and no MCP server, and gemini's own `gemini mcp list` said
// "No MCP servers configured" on a machine that had just run the installer.
// Whoever adds the next -auto target will not think of this either, so it is
// checked for all of them: if the plain target writes an MCP entry, the -auto
// one must too.
func TestAutoTargetsCarryTheirMCPServer(t *testing.T) {
	for _, target := range installTargetNames() {
		base, ok := strings.CutSuffix(target, "-auto")
		if !ok {
			continue
		}
		if base == "claude" {
			base = "claude-code" // detection reports it under the other name
		}
		if !hasTarget(base) {
			continue
		}
		t.Run(target, func(t *testing.T) {
			plain := installedFiles(t, base)
			auto := installedFiles(t, target)

			for path, body := range plain {
				if !strings.Contains(body, "deja") || !strings.Contains(body, "mcp") {
					continue
				}
				got, ok := auto[path]
				if !ok || !strings.Contains(got, "mcp") {
					t.Errorf("%s writes an MCP server into %s and %s does not; "+
						"`deja install --auto` installs only the second one",
						base, path, target)
				}
			}
		})
	}
}

func hasTarget(name string) bool {
	for _, t := range installTargetNames() {
		if t == name {
			return true
		}
	}
	return false
}

// installedFiles installs one target into an empty home and returns what it
// wrote, keyed by path relative to that home.
func installedFiles(t *testing.T, target string) map[string]string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))
	if _, err := installTarget(target, "/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install %s: %v", target, err)
	}
	out := map[string]string{}
	_ = filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		rel, rerr := filepath.Rel(home, path)
		if rerr != nil {
			rel = path
		}
		out[rel] = string(b)
		return nil
	})
	return out
}
