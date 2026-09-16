package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Cherry Studio keeps its MCP servers in its own SQLite store, so there is no
// config file to write and writing into a running app's database is not an
// installer's job. What it has is Settings → MCP → Import from JSON, so deja
// writes the file that import takes and says where to point it — the shape the
// aider target already uses for a file the tool will not fetch itself (#3644).
func TestInstallCherryStudioWritesAnImportableServer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", home+"/.config")

	res, err := installCherryStudio("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action == "unchanged" {
		t.Fatalf("install changed nothing: %+v", res)
	}
	if !strings.Contains(res.Note, "Settings") {
		t.Errorf("note = %q, want it to say where to import the file", res.Note)
	}

	b, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("import file: %v", err)
	}
	var cfg struct {
		Servers map[string]struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("the import file is not JSON: %v", err)
	}
	srv, ok := cfg.Servers["deja"]
	if !ok {
		t.Fatalf("no deja server in %v", cfg.Servers)
	}
	if srv.Type != "stdio" {
		t.Errorf("type = %q, want stdio", srv.Type)
	}
	if srv.Command == "" || len(srv.Args) == 0 || srv.Args[len(srv.Args)-1] != "mcp" {
		t.Errorf("entry = %q %v, want the binary and `mcp`", srv.Command, srv.Args)
	}

	// The note stays on a second run: the file being unchanged does not mean
	// anyone has imported it yet.
	again, err := installCherryStudio("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if again.Action != "unchanged" {
		t.Errorf("second install = %q, want unchanged", again.Action)
	}
	if !strings.Contains(again.Note, "Import from JSON") {
		t.Errorf("the second run dropped the instruction: %q", again.Note)
	}

	out, err := installCherryStudio("/usr/local/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Action != "removed" {
		t.Errorf("uninstall = %q, want removed", out.Action)
	}
	if _, err := os.Stat(res.Path); err == nil {
		t.Errorf("the import file survived uninstall: %s", res.Path)
	}
}
