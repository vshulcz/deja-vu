package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A build called anything but `deja` is what `go build -o /tmp/deja-probe` and
// every install run from it leave behind, and an entry naming one was invisible
// to every check: the row said `wired` because the server key is `deja`, and
// both binary checks stayed silent because the name was unknown (#3659).
func TestDejaCommandInReadsAnEntryUnderDejasOwnKey(t *testing.T) {
	dir := t.TempDir()

	// JSON, the shape most hosts use.
	jsonPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(jsonPath, []byte(`{"mcpServers":{"deja":{"command":"/tmp/probe/deja-cont","args":["mcp"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(jsonPath); got != "/tmp/probe/deja-cont" {
		t.Errorf("json: dejaCommandIn = %q, want the command under the deja key", got)
	}

	// YAML, which is where this was found.
	yamlPath := filepath.Join(dir, "config.yaml")
	yaml := "mcp:\n  servers:\n    deja:\n      command: \"/tmp/probe/deja-cont\"\n      args:\n        - mcp\n"
	if err := os.WriteFile(yamlPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(yamlPath); got != "/tmp/probe/deja-cont" {
		t.Errorf("yaml: dejaCommandIn = %q, want the command under the deja key", got)
	}

	// TOML's table header spelling of the same claim.
	tomlPath := filepath.Join(dir, "config.toml")
	toml := "[mcp_servers.deja]\ncommand = \"/tmp/probe/deja-cont\"\nargs = [\"mcp\"]\n"
	if err := os.WriteFile(tomlPath, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(tomlPath); got != "/tmp/probe/deja-cont" {
		t.Errorf("toml: dejaCommandIn = %q, want the command under the deja key", got)
	}

	// And somebody else's server keeps its own command out of this.
	otherPath := filepath.Join(dir, "other.json")
	if err := os.WriteFile(otherPath, []byte(`{"mcpServers":{"memory":{"command":"/tmp/other/thing"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(otherPath); got != "" {
		t.Errorf("another server's command was read as deja's: %q", got)
	}
}

// With the command readable, the report can say what it says about any other
// stale path.
func TestAStrangeNamedBuildIsReported(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if err := os.WriteFile(filepath.Join(dir, "deja"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(dir, "scratch", "deja-cont")
	if err := os.MkdirAll(filepath.Dir(stray), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(cfg, []byte(`{"mcpServers":{"deja":{"command":"`+stray+`","args":["mcp"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	note := otherBinaryNote(cfg, "hermes")
	if note == "" || !strings.Contains(note, stray) {
		t.Errorf("note = %q, want it to name %s", note, stray)
	}
}
