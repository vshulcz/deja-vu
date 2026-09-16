package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Kilo Code takes the MCP server in `<globalStorage>/kilocode.kilo-code/settings/mcp_settings.json`
// — the file Kilo's own KilocodePaths names — and the shared manual in
// ~/.kilocode/skills, which is where its loader looks first. It has no hooks,
// so those two are the whole wiring (#3643).
func TestInstallKilocodeWiresTheServerAndTheSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, "Code", "User", "globalStorage", "kilocode.kilo-code")
	if err := os.MkdirAll(filepath.Join(root, "settings"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_KILO_ROOTS", root)

	res, err := installKilocode("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action == "unchanged" {
		t.Fatalf("install changed nothing: %+v", res)
	}

	settings := filepath.Join(root, "settings", "mcp_settings.json")
	b, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("mcp settings: %v", err)
	}
	var cfg struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("mcp settings are not the shape Kilo reads: %v", err)
	}
	srv, ok := cfg.Servers["deja"]
	if !ok {
		t.Fatalf("no deja server in %v", cfg.Servers)
	}
	// windows gets the `cmd /c <exe> mcp` shim, so the binary is an argument
	// there rather than the command: what has to hold on every platform is that
	// the entry ends in `mcp` and names this executable.
	if len(srv.Args) == 0 || srv.Args[len(srv.Args)-1] != "mcp" {
		t.Errorf("server entry = %q %v, want it to end in `mcp`", srv.Command, srv.Args)
	}
	if !strings.Contains(srv.Command+" "+strings.Join(srv.Args, " "), "/usr/local/bin/deja") {
		t.Errorf("server entry = %q %v, want the binary in it", srv.Command, srv.Args)
	}

	skill := filepath.Join(home, ".kilocode", "skills", "deja-search", "SKILL.md")
	sb, err := os.ReadFile(skill)
	if err != nil {
		t.Fatalf("skill: %v", err)
	}
	if !strings.Contains(string(sb), "deja") {
		t.Errorf("the skill does not mention deja: %q", string(sb)[:60])
	}

	// Idempotent: a second run reports nothing changed rather than rewriting.
	again, err := installKilocode("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if again.Action != "unchanged" {
		t.Errorf("second install = %q, want unchanged", again.Action)
	}

	// And the uninstall takes both back out.
	out, err := installKilocode("/usr/local/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Action == "unchanged" {
		t.Errorf("uninstall changed nothing: %+v", out)
	}
	if b, err := os.ReadFile(settings); err == nil && strings.Contains(string(b), "deja") {
		t.Errorf("the server is still wired after uninstall: %s", b)
	}
	if _, err := os.Stat(skill); err == nil {
		t.Errorf("the skill survived uninstall: %s", skill)
	}
}

// A machine with VS Code but without the extension is not a Kilo machine: the
// installer must not create settings for an editor that never had it.
func TestInstallKilocodeSkipsAHostWithoutTheExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_KILO_ROOTS", filepath.Join(home, "Code", "User", "globalStorage", "kilocode.kilo-code"))

	res, err := installKilocode("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	// The skill is still written — the CLI reads the same directory — but the
	// note has to say the server was not wired anywhere.
	if !strings.Contains(res.Note, "no VS Code host") {
		t.Errorf("note = %q, want it to say no host has Kilo Code", res.Note)
	}
	if _, err := os.Stat(kilocodeSkillPath()); err != nil {
		t.Errorf("the skill was not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "Code")); err == nil {
		t.Error("the installer created an editor directory that was not there")
	}
}
