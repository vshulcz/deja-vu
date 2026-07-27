package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallHermesPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_HERMES_HOME", "")
	if _, err := installHermesAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	dir := filepath.Join(home, ".hermes", "plugins", "deja")
	manifest, err := os.ReadFile(filepath.Join(dir, "plugin.yaml"))
	if err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	// Hermes reads the manifest to decide what the plugin provides.
	if !strings.Contains(string(manifest), "pre_llm_call") {
		t.Fatalf("manifest does not declare the injecting hook:\n%s", manifest)
	}
	code, err := os.ReadFile(filepath.Join(dir, "__init__.py"))
	if err != nil {
		t.Fatalf("plugin code missing: %v", err)
	}
	src := string(code)
	for _, want := range []string{
		`ctx.register_hook("pre_llm_call"`, // the only hook that can inject
		"ctx.register_command",             // /deja, so the tool is findable
		"hook-context",                     // first turn: the session digest
		"hook-prompt",                      // later turns: relevance
		`"/bin/deja"`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("plugin missing %q:\n%s", want, src)
		}
	}
	cfg, err := os.ReadFile(filepath.Join(home, ".hermes", "config.yaml"))
	if err != nil {
		t.Fatalf("config missing: %v", err)
	}
	// Discovered is not loaded: Hermes lists the plugin as "not enabled" until
	// its name is in plugins.enabled.
	if !strings.Contains(string(cfg), "plugins:\n  enabled:\n    - deja") {
		t.Fatalf("plugin never enabled:\n%s", cfg)
	}
	if !strings.Contains(string(cfg), "mcp_servers:\n  deja:") {
		t.Fatalf("MCP server not registered:\n%s", cfg)
	}
}

func TestInstallHermesUninstallLeavesNeighbours(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_HERMES_HOME", "")
	dir := filepath.Join(home, ".hermes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := "# Hermes config\nplugins:\n  enabled:\n    - other\n  disabled: []\nmcp_servers:\n  github:\n    command: gh\n    enabled: true\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installHermesAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := installHermesAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	cfg := string(b)
	if strings.Contains(cfg, "deja") {
		t.Fatalf("uninstall left deja behind:\n%s", cfg)
	}
	for _, keep := range []string{"# Hermes config", "- other", "github:", "command: gh"} {
		if !strings.Contains(cfg, keep) {
			t.Fatalf("uninstall dropped %q:\n%s", keep, cfg)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "plugins", "deja")); !os.IsNotExist(err) {
		t.Fatalf("plugin directory survived: %v", err)
	}
}

// A hook must not stall a turn, but a command the user typed can wait: the
// first search of a session may rebuild the index, which takes far longer than
// the hook budget. With one shared timeout /deja answered "nothing matches"
// for a query that had thirty-six hits.
func TestHermesCommandGetsALongerTimeoutThanTheHook(t *testing.T) {
	src := hermesPluginPy("/bin/deja")
	if !strings.Contains(src, "timeout=120") {
		t.Fatalf("the slash command still runs on the hook budget:\n%s", src)
	}
	if !strings.Contains(src, "def _deja(args, payload=\"\", timeout=10)") {
		t.Fatalf("the hook budget is no longer the default:\n%s", src)
	}
}
