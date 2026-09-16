package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each of the three writes the server where that tool's own source says it
// reads one, and says the one thing the file cannot say for itself (#3651).
func TestInstallKiroWritesTheGlobalSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	res, err := installKiro("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".kiro", "settings", "mcp.json")
	if res.Path != want {
		t.Errorf("path = %q, want %q", res.Path, want)
	}
	assertMCPServerEntry(t, want)
	// A custom agent does not inherit global servers, and a reader who has one
	// would otherwise find recall missing with nothing saying why.
	if !strings.Contains(res.Note, "agents") {
		t.Errorf("note = %q, want it to mention custom agents", res.Note)
	}

	out, err := installKiro("/usr/local/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Action == "unchanged" {
		t.Errorf("uninstall changed nothing: %+v", out)
	}
	if b, err := os.ReadFile(want); err == nil && strings.Contains(string(b), "deja") {
		t.Errorf("the server is still wired after uninstall: %s", b)
	}
}

func TestInstallKimchiWritesTheAgentDirServer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("KIMCHI_CODING_AGENT_DIR", "")

	res, err := installKimchi("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".config", "kimchi", "harness", "mcp.json")
	if res.Path != want {
		t.Errorf("path = %q, want %q", res.Path, want)
	}
	assertMCPServerEntry(t, want)
	// Both of Kimchi's compatibility extensions ship disabled, so nothing
	// arrives on its own and the note has to name the way in.
	if !strings.Contains(res.Note, "claude-code-hook-adapter") {
		t.Errorf("note = %q, want the command that turns recall on", res.Note)
	}

	// KIMCHI_CODING_AGENT_DIR moves the whole agent directory, and the
	// installer has to follow it or it writes where nothing reads.
	moved := filepath.Join(home, "moved-agent")
	t.Setenv("KIMCHI_CODING_AGENT_DIR", moved)
	res, err = installKimchi("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != filepath.Join(moved, "mcp.json") {
		t.Errorf("path = %q, want it under the relocated agent dir", res.Path)
	}
}

func TestInstallGjcWritesTheServerAndTheNativeSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GJC_CODING_AGENT_DIR", "")

	res, err := installGjc("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action == "unchanged" {
		t.Fatalf("install changed nothing: %+v", res)
	}
	assertMCPServerEntry(t, filepath.Join(home, ".gjc", "agent", "mcp.json"))

	// gjc loads its own skills directory; Claude's and Codex's are import
	// candidates there, so a skill written to those would never be read.
	skill := filepath.Join(home, ".gjc", "agent", "skills", "deja-search", "SKILL.md")
	b, err := os.ReadFile(skill)
	if err != nil {
		t.Fatalf("skill: %v", err)
	}
	if !strings.Contains(string(b), "deja") {
		t.Errorf("the skill does not mention deja: %q", string(b)[:60])
	}

	if again, err := installGjc("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	} else if again.Action != "unchanged" {
		t.Errorf("second install = %q, want unchanged", again.Action)
	}

	if _, err := installGjc("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(skill); err == nil {
		t.Errorf("the skill survived uninstall: %s", skill)
	}
}

// assertMCPServerEntry is the shape every client here reads: a `mcpServers`
// block with deja in it, ending in `mcp`. windows gets the `cmd /c` shim, so
// the binary may be an argument rather than the command.
func assertMCPServerEntry(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var cfg struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("%s is not the shape the client reads: %v", path, err)
	}
	srv, ok := cfg.Servers["deja"]
	if !ok {
		t.Fatalf("%s: no deja server in %v", path, cfg.Servers)
	}
	if len(srv.Args) == 0 || srv.Args[len(srv.Args)-1] != "mcp" {
		t.Errorf("%s: entry = %q %v, want it to end in `mcp`", path, srv.Command, srv.Args)
	}
	if !strings.Contains(srv.Command+" "+strings.Join(srv.Args, " "), "deja") {
		t.Errorf("%s: entry = %q %v, want the binary in it", path, srv.Command, srv.Args)
	}
}
