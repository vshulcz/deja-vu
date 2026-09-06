package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ampSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("settings not written: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("settings are not JSON Amp can read: %v\n%s", err, b)
	}
	return root
}

// Amp keeps MCP servers under one literal key with a dot in its name. Writing a
// nested {"amp": {"mcpServers": …}} instead produces a file Amp parses happily
// and ignores completely.
func TestInstallAmpWritesTheDottedServerKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AMP_SETTINGS_FILE", "")

	r, err := installAmpMCP("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".config", "amp", "settings.json")
	if r.Path != want {
		t.Fatalf("wrote %q, want %q", r.Path, want)
	}
	root := ampSettings(t, r.Path)
	if _, nested := root["amp"]; nested {
		t.Fatalf("wrote a nested amp object; Amp reads the flat %q key:\n%v", ampServersKey, root)
	}
	servers, _ := root[ampServersKey].(map[string]any)
	entry, _ := servers["deja"].(map[string]any)
	if entry == nil {
		t.Fatalf("no deja server under %q: %v", ampServersKey, root)
	}
	if entry["command"] == nil || entry["args"] == nil {
		t.Fatalf("server entry is missing command/args: %v", entry)
	}
}

// AMP_SETTINGS_FILE moves the file Amp itself reads, so an install that ignores
// it writes somewhere nothing loads.
func TestInstallAmpFollowsTheSettingsFileOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	elsewhere := filepath.Join(home, "elsewhere", "settings.json")
	t.Setenv("AMP_SETTINGS_FILE", elsewhere)

	r, err := installAmpMCP("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Path != elsewhere {
		t.Fatalf("wrote %q, want %q", r.Path, elsewhere)
	}
	ampSettings(t, elsewhere)
}

// Someone else's servers and settings have to survive both directions.
func TestInstallAmpKeepsOtherSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, "settings.json")
	t.Setenv("AMP_SETTINGS_FILE", path)
	before := `{"amp.mcpServers":{"context7":{"command":"npx","args":["-y","@upstash/context7-mcp"]}},"amp.notifications.enabled":true}`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installAmpMCP("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	root := ampSettings(t, path)
	servers, _ := root[ampServersKey].(map[string]any)
	if servers["context7"] == nil || servers["deja"] == nil {
		t.Fatalf("install did not sit beside the existing server: %v", root)
	}
	if root["amp.notifications.enabled"] != true {
		t.Fatalf("an unrelated setting was dropped: %v", root)
	}
	if _, err := installAmpMCP("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	root = ampSettings(t, path)
	servers, _ = root[ampServersKey].(map[string]any)
	if servers["deja"] != nil {
		t.Fatalf("uninstall left the deja entry: %v", root)
	}
	if servers["context7"] == nil {
		t.Fatalf("uninstall took someone else's server with it: %v", root)
	}
}

func TestInstallAmpAutoWritesADiscoverablePlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AMP_SETTINGS_FILE", "")

	if _, err := installAmpAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	// Amp auto-discovers ~/.config/amp/plugins/*.ts; anywhere else is inert.
	path := filepath.Join(home, ".config", "amp", "plugins", "deja.ts")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("plugin not at the discovered path: %v", err)
	}
	src := string(b)
	for _, want := range []string{
		`amp.on("agent.start"`, // the one event whose return reaches the model
		"hook-context",
		`"hook-prompt", "--plain"`,
		`amp.on("tool.result"`,
		`"hook-tool-after", "--plain"`,
		"registerCommand",
		`"/usr/local/bin/deja"`,
		"export const description", // Amp shows this in plugin settings
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("plugin missing %q:\n%s", want, src)
		}
	}
	// The MCP half goes in at the same time: a plugin that hands the model a
	// digest and no tool to follow it up with is half an install.
	root := ampSettings(t, filepath.Join(home, ".config", "amp", "settings.json"))
	if root[ampServersKey] == nil {
		t.Fatalf("amp-auto wrote no MCP server: %v", root)
	}
}

func TestUninstallAmpAutoRemovesThePlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AMP_SETTINGS_FILE", "")

	if _, err := installAmpAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := installAmpAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".config", "amp", "plugins", "deja.ts")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("plugin survived uninstall: %v", err)
	}
	// Uninstalling twice is not an error and creates nothing.
	if r, err := installAmpPlugin("/usr/local/bin/deja", true); err != nil || r.Action != "unchanged" {
		t.Fatalf("second uninstall = %+v, %v", r, err)
	}
}

func TestAmpPluginQuotesTheBinaryPath(t *testing.T) {
	src := ampPluginTS(`C:\Program Files\deja\deja.exe`)
	if !strings.Contains(src, `const DEJA = "C:\\Program Files\\deja\\deja.exe"`) {
		t.Fatalf("a Windows path was not escaped for TypeScript:\n%s", src)
	}
}

// Amp's tool.result carries a status, not an isError flag, and a repair sent
// back with status "done" would tell the model a failed command succeeded.
func TestAmpRepairKeepsTheFailureStatus(t *testing.T) {
	src := ampPluginTS("/bin/deja")
	if !strings.Contains(src, `event.status !== "error"`) {
		t.Fatalf("the repair does not gate on a failed tool:\n%s", src)
	}
	if !strings.Contains(src, `return { status: "error"`) {
		t.Fatalf("the repair reports the failure as something other than an error:\n%s", src)
	}
	if !strings.Contains(src, "event.toolUseID") {
		t.Fatalf("the lookup is not keyed per tool call:\n%s", src)
	}
}

// A search the user typed may rebuild the index; a hook may not. One shared
// timeout would make the command answer "nothing matches" for a query with
// real hits, and a query starting with a dash must not be read as a flag.
func TestAmpCommandOutlivesTheHookTimeoutAndSurvivesAFlagQuery(t *testing.T) {
	src := ampPluginTS("/bin/deja")
	if !strings.Contains(src, "120000") || !strings.Contains(src, "timeout = 10000") {
		t.Fatalf("the command runs on the hook budget:\n%s", src)
	}
	if !strings.Contains(src, `["search", "--", query]`) {
		t.Fatalf("a query that names a deja flag dies in flag parsing:\n%s", src)
	}
}
