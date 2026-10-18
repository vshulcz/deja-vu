package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A config the reader made read-only was rewritten anyway: the temp file is
// renamed over it, which the file's own mode never stops, and the result kept
// 0444 (#4558). Refused now, and the file is left as it was.
func TestInstallRefusesAReadOnlyConfig(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the file mode is not enforced here")
	}
	for _, c := range []struct {
		target, seed string
		path         func() string
	}{
		{"cursor", "{\n  \"mcpServers\": {}\n}\n", func() string { return filepath.Join(sources.CursorCLIHome(), "mcp.json") }},
		{"codex", "model = \"o3\"\n", func() string { return filepath.Join(sources.CodexHome(), "config.toml") }},
		{"hermes", "model: hermes-3\n", func() string { return filepath.Join(sources.HermesHome(), "config.yaml") }},
	} {
		t.Run(c.target, func(t *testing.T) {
			hermeticEnv(t)
			p := c.path()
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(c.seed), 0o444); err != nil {
				t.Fatal(err)
			}
			_, err := captureRun(t, "install", c.target, "--no-index", "--no-guidance")
			if err == nil || !strings.Contains(err.Error(), "read-only") {
				t.Errorf("install did not refuse the read-only config: %v", err)
			}
			if got, _ := os.ReadFile(p); string(got) != c.seed {
				t.Errorf("a read-only config was rewritten:\n%s", got)
			}
			if _, err := os.Stat(p + ".bak"); err == nil {
				t.Errorf("a snapshot was taken of a config deja did not change")
			}
		})
	}
}

// A target that edits two files refuses on the read-only one before writing
// the other.
func TestAutoTargetWithAReadOnlyHooksFileWritesNothing(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the file mode is not enforced here")
	}
	hermeticEnv(t)
	hooks := filepath.Join(sources.CodexHome(), "hooks.json")
	cfg := filepath.Join(sources.CodexHome(), "config.toml")
	if err := os.MkdirAll(filepath.Dir(hooks), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hooks, []byte("{}\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	const seed = "model = \"o3\"\n"
	if err := os.WriteFile(cfg, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "codex-auto", "--no-index", "--no-guidance"); err == nil {
		t.Fatal("install wrote through a read-only hooks.json")
	}
	if got, _ := os.ReadFile(cfg); string(got) != seed {
		t.Errorf("a refused target wrote config.toml:\n%s", got)
	}
}

// hermes-auto on a read-only config.yaml whose MCP entry is already right:
// the MCP step has nothing to write, so the refusal came from plugins.enabled
// after the plugin files were already in.
func TestHermesAutoOnAReadOnlyConfigWritesNoPluginFiles(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the file mode is not enforced here")
	}
	hermeticEnv(t)
	cfg := filepath.Join(sources.HermesHome(), "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("model: hermes-3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "hermes", "--no-index", "--no-guidance"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfg, 0o444); err != nil {
		t.Fatal(err)
	}
	_, err := captureRun(t, "install", "hermes-auto", "--no-index", "--no-guidance")
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("install did not refuse the read-only config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sources.HermesHome(), "plugins", "deja")); err == nil {
		t.Errorf("a refused install wrote the plugin files")
	}
}
