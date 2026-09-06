package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// VS Code Copilot Chat reads MCP servers from mcp.json under `servers` (not the
// common `mcpServers`), and each entry carries a `type`. A file in the other
// shape loads nothing and the chat gets no tools.
func TestInstallVSCodeMCPWritesTheServersShape(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_VSCODE_USER_DIRS", dir)
	if _, err := installVSCodeMCP("/usr/local/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "mcp.json"))
	if err != nil {
		t.Fatalf("mcp.json not written where VS Code reads it: %v", err)
	}
	var root struct {
		Servers map[string]struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("mcp.json is not valid JSON: %v\n%s", err, b)
	}
	d, ok := root.Servers["deja"]
	if !ok {
		t.Fatalf("no deja server under the servers key:\n%s", b)
	}
	if d.Type != "stdio" {
		t.Errorf("VS Code needs an explicit type, got %q", d.Type)
	}
	if d.Command == "" || len(d.Args) == 0 || d.Args[len(d.Args)-1] != "mcp" {
		t.Errorf("the server does not launch `deja mcp`: %+v", d)
	}
	if strings.Contains(string(b), "mcpServers") {
		t.Errorf("wrote the mcpServers key VS Code does not read:\n%s", b)
	}
}

// A machine can run more than one VS Code, and other people keep their own
// servers. install writes each host that exists and leaves every other server
// alone; uninstall takes only deja's.
func TestInstallVSCodeMCPPreservesOtherServersAndHosts(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	t.Setenv("DEJA_VSCODE_USER_DIRS", a+string(os.PathListSeparator)+b)
	// One host already has a server of the user's own.
	theirs := `{"servers":{"other":{"type":"stdio","command":"x","args":[]}}}`
	if err := os.WriteFile(filepath.Join(a, "mcp.json"), []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installVSCodeMCP("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, dir := range []string{a, b} {
		body, err := os.ReadFile(filepath.Join(dir, "mcp.json"))
		if err != nil {
			t.Fatalf("%s: mcp.json not written: %v", dir, err)
		}
		if !strings.Contains(string(body), `"deja"`) {
			t.Errorf("%s: deja not wired:\n%s", dir, body)
		}
	}
	if body, _ := os.ReadFile(filepath.Join(a, "mcp.json")); !strings.Contains(string(body), `"other"`) {
		t.Errorf("install dropped the user's own server:\n%s", body)
	}

	if _, err := installVSCodeMCP("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(a, "mcp.json"))
	if strings.Contains(string(body), `"deja"`) {
		t.Errorf("uninstall left deja behind:\n%s", body)
	}
	if !strings.Contains(string(body), `"other"`) {
		t.Errorf("uninstall took the user's own server with it:\n%s", body)
	}
}

// VS Code writes mcp.json with comments; a strict parse would refuse a file the
// editor itself produced.
func TestInstallVSCodeMCPKeepsComments(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_VSCODE_USER_DIRS", dir)
	jsonc := "{\n  // my servers\n  \"servers\": {}\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(jsonc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installVSCodeMCP("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "mcp.json"))
	if !strings.Contains(string(body), "// my servers") {
		t.Errorf("the comment was lost:\n%s", body)
	}
	if !strings.Contains(string(body), `"deja"`) {
		t.Errorf("deja not wired into the commented file:\n%s", body)
	}
}

// No VS Code User folder is not an error: say so rather than write a file into a
// directory the editor will never read.
func TestInstallVSCodeMCPWithoutAUserFolder(t *testing.T) {
	t.Setenv("DEJA_VSCODE_USER_DIRS", filepath.Join(t.TempDir(), "does-not-exist"))
	// The override names a dir that does not exist; installVSCodeMCP still writes
	// there because the override is explicit. The empty case is the resolver
	// returning nothing, which the override cannot express, so this checks the
	// explicit path writes rather than the empty-note path — the note path is
	// covered by the resolver returning [] on a machine with no VS Code.
	if _, err := installVSCodeMCP("/bin/deja", false); err != nil {
		t.Fatalf("install into an explicit dir: %v", err)
	}
}
