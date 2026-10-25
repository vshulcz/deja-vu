package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

type cursorPluginHook struct {
	Command string `json:"command"`
	Matcher string `json:"matcher"`
}

type cursorPluginHooks struct {
	Version int                           `json:"version"`
	Hooks   map[string][]cursorPluginHook `json:"hooks"`
}

// The Cursor plugin is what a marketplace install gets instead of
// `deja install cursor-auto`, so its hooks.json has to be the file the
// installer writes: the same events, matchers and subcommands, with the
// bridge in place of the binary.
func TestCursorPluginHooksMatchTheInstaller(t *testing.T) {
	hermeticEnv(t)
	exe := filepath.Join(t.TempDir(), "deja")
	if _, err := cursorHooksUpdate(exe, false, true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(sources.CursorCLIHome(), "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var installed, plugin cursorPluginHooks
	if err := json.Unmarshal(b, &installed); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(repoFile(t, "cursor-plugin/hooks/hooks.json"), &plugin); err != nil {
		t.Fatal(err)
	}
	if plugin.Version != installed.Version {
		t.Errorf("version %d, the installer writes %d", plugin.Version, installed.Version)
	}
	const bridge = `sh "${CURSOR_PLUGIN_ROOT}/hooks/deja.sh" `
	want := map[string][]cursorPluginHook{}
	for event, entries := range installed.Hooks {
		for _, e := range entries {
			fields := strings.Fields(e.Command)
			want[event] = append(want[event], cursorPluginHook{Command: bridge + fields[len(fields)-1], Matcher: e.Matcher})
		}
	}
	if !reflect.DeepEqual(plugin.Hooks, want) {
		t.Fatalf("cursor-plugin/hooks/hooks.json:\n%+v\nthe installer writes:\n%+v", plugin.Hooks, want)
	}
}

// Cursor's plugin schema is closed and the marketplace validates against it:
// an unknown field, a bad name or a path that does not resolve is a rejected
// submission. The marketplace entry has to name the plugin and its directory.
func TestCursorPluginManifest(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal(repoFile(t, "cursor-plugin/.cursor-plugin/plugin.json"), &m); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"name": true, "displayName": true, "description": true, "version": true,
		"minClientVersions": true, "author": true, "publisher": true, "homepage": true,
		"repository": true, "license": true, "logo": true, "keywords": true,
		"category": true, "tags": true, "commands": true, "agents": true,
		"skills": true, "rules": true, "hooks": true, "variables": true, "mcpServers": true,
	}
	for k := range m {
		if !allowed[k] {
			t.Errorf("field %q is not in Cursor's plugin schema", k)
		}
	}
	author, _ := m["author"].(map[string]any)
	if author["name"] == nil {
		t.Errorf("author %v has no name", m["author"])
	}
	for k := range author {
		if k != "name" && k != "email" {
			t.Errorf("author.%s: the schema allows name and email only", k)
		}
	}
	name, _ := m["name"].(string)
	if !regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`).MatchString(name) {
		t.Errorf("name %q does not match Cursor's pattern", name)
	}
	var claude struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(repoFile(t, "claude-plugin/.claude-plugin/plugin.json"), &claude); err != nil {
		t.Fatal(err)
	}
	if m["version"] != claude.Version {
		t.Errorf("version %v, the Claude plugin is at %s", m["version"], claude.Version)
	}
	for _, k := range []string{"logo", "skills", "commands", "hooks", "mcpServers"} {
		p, _ := m[k].(string)
		if p == "" || strings.Contains(p, "..") {
			t.Errorf("%s = %q", k, p)
			continue
		}
		if _, err := os.Stat(filepath.Join("..", "..", "cursor-plugin", filepath.FromSlash(p))); err != nil {
			t.Errorf("%s: %v", k, err)
		}
	}
	for _, p := range []string{"hooks/deja.sh", "mcp/deja-mcp.sh", "README.md", "LICENSE", "SECURITY.md"} {
		if _, err := os.Stat(filepath.Join("..", "..", "cursor-plugin", p)); err != nil {
			t.Errorf("cursor-plugin/%s: %v", p, err)
		}
	}

	var mp struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(repoFile(t, ".cursor-plugin/marketplace.json"), &mp); err != nil {
		t.Fatal(err)
	}
	if len(mp.Plugins) != 1 || mp.Plugins[0].Name != name || mp.Plugins[0].Source != "cursor-plugin" {
		t.Errorf("marketplace plugins = %+v, want %s from cursor-plugin", mp.Plugins, name)
	}

	var mcp struct {
		Servers map[string]pluginMCPServer `json:"mcpServers"`
	}
	if err := json.Unmarshal(repoFile(t, "cursor-plugin/mcp.json"), &mcp); err != nil {
		t.Fatal(err)
	}
	if s := mcp.Servers["deja"]; s.Command != "sh" || len(s.Args) != 1 || s.Args[0] != "${CURSOR_PLUGIN_ROOT}/mcp/deja-mcp.sh" {
		t.Errorf("mcp server = %+v", mcp.Servers)
	}
}

// Without the binary the bridge prints nothing and exits 0: Cursor shows a
// hook's failure to the user, and the plugin is often installed first.
func TestCursorPluginBridgeSilentWithoutDeja(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the bridge is an sh script")
	}
	for _, p := range []string{"/opt/homebrew/bin/deja", "/usr/local/bin/deja"} {
		if _, err := os.Stat(p); err == nil {
			t.Skipf("%s exists, so the bridge would find it", p)
		}
	}
	hermeticEnv(t)
	cmd := exec.CommandContext(t.Context(), "sh", filepath.Join("..", "..", "cursor-plugin", "hooks", "deja.sh"), "hook-context")
	cmd.Env = append(os.Environ(), "PATH=/usr/bin:/bin")
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"sessionStart"}`)
	out, err := cmd.CombinedOutput()
	if err != nil || len(out) != 0 {
		t.Fatalf("bridge without deja: err %v, output %q", err, out)
	}
}

// Cursor runs the hooks claude-auto wrote into ~/.claude/settings.json as well
// as the plugin's, and dedupes only identical commands, so the bridge has to
// stand down there too or every session gets the digest twice.
func TestCursorPluginBridgeStandsDownForClaudeAuto(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the bridge is an sh script")
	}
	hermeticEnv(t)
	home := os.Getenv("HOME")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "deja"), []byte("#!/bin/sh\necho ran\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func() string {
		cmd := exec.CommandContext(t.Context(), "sh", filepath.Join("..", "..", "cursor-plugin", "hooks", "deja.sh"), "hook-context")
		cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin", "CLAUDE_CONFIG_DIR=", "CURSOR_CONFIG_DIR=")
		cmd.Stdin = strings.NewReader(`{"hook_event_name":"sessionStart"}`)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bridge: %v %q", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if got := run(); got != "ran" {
		t.Fatalf("without claude-auto the bridge printed %q, want deja to run", got)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/x/deja-hook hook-context"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := run(); got != "" {
		t.Fatalf("with claude-auto wired the bridge printed %q, want nothing", got)
	}
}
