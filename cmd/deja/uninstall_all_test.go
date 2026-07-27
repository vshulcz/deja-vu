package main

import (
	"github.com/vshulcz/deja-vu/internal/index"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Someone who uninstalls deja and then deletes the binary must not be left
// with agents shelling out to a path that is gone. `--all` used to expand to
// the MCP targets only, so every hook and plugin `--auto` had written stayed.
func TestUninstallAllRemovesHooksAndPlugins(t *testing.T) {
	home := hermeticEnv(t)
	home = filepath.Join(home, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	// Only harnesses with a config directory are detected, so create the ones
	// whose auto-install writes a separate file worth checking.
	for _, dir := range []string{
		".claude", ".codex", ".config/opencode/opencode", ".cursor",
		".qwen", ".kimi-code", ".pi/agent", ".cline/data",
	} {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runInstall(index.DefaultDir(), []string{"--auto"}, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	written := []string{
		filepath.Join(home, ".config", "opencode", "plugins", "deja.js"),
		filepath.Join(home, ".cline", "plugins", "deja", "index.js"),
		filepath.Join(home, ".pi", "agent", "extensions", "deja.ts"),
		filepath.Join(home, ".claude", "commands", "deja.md"),
	}
	for _, p := range written {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("install --auto did not write %s: %v", p, err)
		}
	}
	if err := runInstall(index.DefaultDir(), []string{"--all"}, true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	for _, p := range written {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("uninstall --all left %s behind", p)
		}
	}
	// Config files stay, but nothing in them may still call deja.
	for _, p := range []string{
		filepath.Join(home, ".kimi-code", "config.toml"),
		filepath.Join(home, ".qwen", "settings.json"),
		filepath.Join(home, ".cursor", "hooks.json"),
	} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if strings.Contains(string(b), "hook-context") || strings.Contains(string(b), "hook-prompt") {
			t.Fatalf("%s still wires a deja hook after uninstall --all:\n%s", p, b)
		}
	}
}

// The expansion is the part that was wrong, so pin it directly: every target
// with an -auto sibling must gain it, and Claude's is not named after the
// harness the detector reports.
func TestWithAutoTargetsPairsEveryTarget(t *testing.T) {
	got := withAutoTargets([]string{"claude-code", "cline", "opencode", "grok"})
	want := map[string]bool{
		"claude-code": true, "claude-auto": true,
		"cline": true, "cline-auto": true,
		"opencode": true, "opencode-auto": true,
		"grok": true,
	}
	for _, g := range got {
		if !want[g] {
			t.Fatalf("unexpected target %q in %v", g, got)
		}
		delete(want, g)
	}
	if len(want) != 0 {
		t.Fatalf("missing targets %v (got %v)", want, got)
	}
	// grok has no -auto target; asking for one would be an unknown-target error.
	for _, g := range got {
		if g == "grok-auto" {
			t.Fatal("invented an -auto target that does not exist")
		}
	}
}
