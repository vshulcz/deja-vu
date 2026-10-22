package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// devinTestHome isolates a test from the machine's real Devin CLI config:
// HOME and the override envs all point at one temporary root.
func devinTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	return home
}

func TestDevinConfigDirFollowsThePlatformConventions(t *testing.T) {
	home := devinTestHome(t)
	for _, c := range []struct {
		name string
		goos string
		xdg  string
		app  string
		want string
	}{
		{"linux default", "linux", "", "", filepath.Join(home, ".config", "devin")},
		{"linux xdg", "linux", "/tmp/xdg-test", "", filepath.Join("/tmp/xdg-test", "devin")},               //nolint:gocritic // the want is joined the way the code joins it
		{"macos xdg honoured", "darwin", "/tmp/xdg-mac", "", filepath.Join("/tmp/xdg-mac", "devin")},       //nolint:gocritic // the want is joined the way the code joins it
		{"windows appdata", "windows", "", "C:\\win\\roaming", filepath.Join("C:\\win\\roaming", "devin")}, //nolint:gocritic // the want is joined the way the code joins it
		{"windows fallback", "windows", "", "", filepath.Join(home, "AppData", "Roaming", "devin")},
	} {
		t.Setenv("XDG_CONFIG_HOME", c.xdg)
		t.Setenv("APPDATA", c.app)
		if got := devinConfigDirFor(c.goos); got != c.want {
			t.Errorf("%s: devinConfigDirFor(%q) = %q, want %q", c.name, c.goos, got, c.want)
		}
	}
}

// installDevinHooks writes every wired event with its matcher through the
// same maintainer claude's install uses, and uninstall takes them back out.
func TestInstallDevinHooksRoundTrip(t *testing.T) {
	devinTestHome(t)
	res, err := installDevinHooks("deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action == "unchanged" {
		t.Fatal("first install should have written config.json")
	}
	raw, err := os.ReadFile(devinConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if uerr := json.Unmarshal(raw, &root); uerr != nil {
		t.Fatal(uerr)
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("config has no hooks object: %s", raw)
	}
	for _, h := range devinHookWiring {
		entries, ok := hooks[h.Event].([]any)
		if !ok || len(entries) == 0 {
			t.Errorf("event %s missing from config.json", h.Event)
			continue
		}
		blob, jerr := json.Marshal(entries)
		if jerr != nil {
			t.Fatal(jerr)
		}
		if h.Matcher != "" && !strings.Contains(string(blob), h.Matcher) {
			t.Errorf("event %s lost its matcher %q", h.Event, h.Matcher)
		}
		if !strings.Contains(string(blob), h.Sub) {
			t.Errorf("event %s does not run %q", h.Event, h.Sub)
		}
	}
	seen := map[string]bool{}
	for _, h := range devinHookWiring {
		seen[h.Event] = true
	}
	if len(hooks) != len(seen) {
		t.Errorf("hooks object has %d events, want %d", len(hooks), len(seen))
	}

	res, err = installDevinHooks("deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "unchanged" {
		t.Errorf("second install rewrote what it already wrote: %v", res)
	}
	if _, err := installDevinHooks("deja", true); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(devinConfigPath())
	if strings.Contains(string(raw), "hook-context") {
		t.Errorf("uninstall left deja wiring behind: %s", raw)
	}
}

// installDevinMCP puts the server into mcp_config.json and the shared skill
// into ~/.agents/skills — both come back out on uninstall.
func TestInstallDevinMCPRoundTrip(t *testing.T) {
	devinTestHome(t)
	if _, err := installDevinMCP("deja", false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(devinMCPConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	srvs, _ := root["mcpServers"].(map[string]any)
	deja, ok := srvs["deja"].(map[string]any)
	if !ok {
		t.Fatalf("mcp_config.json has no deja server: %s", raw)
	}
	// The subcommand is the last word whether the command runs the binary
	// directly (posix) or through cmd /c on Windows; flatten both shapes.
	words := strings.Fields(fmt.Sprint(deja["command"]))
	if a, ok := deja["args"].([]any); ok {
		for _, v := range a {
			words = append(words, strings.Fields(fmt.Sprint(v))...)
		}
	}
	if len(words) == 0 || words[len(words)-1] != "mcp" {
		t.Errorf("deja server does not run `deja mcp`: %v", deja)
	}
	if _, err := os.Stat(sharedSkillPath()); err != nil {
		t.Errorf("skill file was not written to %s: %v", sharedSkillPath(), err)
	}
	if _, err := installDevinMCP("deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sharedSkillPath()); !os.IsNotExist(err) {
		t.Errorf("skill file survived uninstall")
	}
}

// Both install paths must surface a config that cannot be written rather
// than swallow it: a mangled config.json, and an mcp_config.json that is not
// a file at all.
func TestInstallDevinSurfacesConfigErrors(t *testing.T) {
	devinTestHome(t)
	if err := os.MkdirAll(filepath.Dir(devinConfigPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(devinConfigPath(), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installDevinAuto("deja", false); err == nil {
		t.Error("a config.json that cannot be parsed must fail the install")
	}
	if err := os.Remove(devinConfigPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(devinMCPConfigPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installDevinMCP("deja", false); err == nil {
		t.Error("an mcp_config.json that is a directory must fail the install")
	}
	if _, err := installDevinAuto("deja", false); err == nil {
		t.Error("devin-auto must surface the MCP failure even when hooks wrote fine")
	}
	if err := os.Remove(devinMCPConfigPath()); err != nil {
		t.Fatal(err)
	}
	// The MCP file writing fine is not enough: the shared skill has to land
	// too, and a path that cannot be made is an error, not silence. A plain
	// file where a directory belongs makes MkdirAll fail.
	skillsDir := filepath.Dir(filepath.Dir(sharedSkillPath()))
	if err := os.MkdirAll(filepath.Dir(skillsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillsDir, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installDevinMCP("deja", false); err == nil {
		t.Error("an unwritable skill path must fail the install")
	}
}

// devin-auto is both layers in one entry: hooks plus the MCP server.
func TestInstallDevinAutoWiresBothLayers(t *testing.T) {
	devinTestHome(t)
	if _, err := installDevinAuto("deja", false); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{devinConfigPath(), devinMCPConfigPath()} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("devin-auto left %s unwritten: %v", path, err)
		}
	}
	if _, err := installDevinAuto("deja", true); err != nil {
		t.Fatal(err)
	}
}
