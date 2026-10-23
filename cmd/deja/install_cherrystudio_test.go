package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	if uerr := json.Unmarshal(b, &cfg); uerr != nil {
		t.Fatalf("the import file is not JSON: %v", uerr)
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

// Cherry Studio's importer validates the pasted JSON with a strict schema, so
// a key it does not know fails the whole import rather than being ignored.
// Read out of the app bundle (2.0.14,
// out/renderer/assets/mcp-uMWTyAhi.js): `McpConfigSchema = object({ mcpServers:
// record(string(), McpServerConfigSchema) })`, where the server schema is
// `object({… type, command, args, env, headers, …}).strict()` and `type` is one
// of stdio, sse, streamableHttp, inMemory. The importer names the server after
// the key unless the entry carries its own `name`.
//
// So the file deja writes has to stay exactly these three keys under exactly
// that envelope. This test is the reminder, since nothing else here would fail
// if a fourth key were added (#3674).
func TestCherryStudioImportFileMatchesTheAppsStrictSchema(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if _, err := installCherryStudio("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(cherryStudioImportPath())
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]map[string]map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("the import file is not the shape the app parses: %v", err)
	}
	servers, ok := root["mcpServers"]
	if !ok || len(servers) != 1 {
		t.Fatalf("want one server under mcpServers, got %v", root)
	}
	entry, ok := servers["deja"]
	if !ok {
		t.Fatalf("the server is not keyed `deja`, so the app would name it something else: %v", servers)
	}
	allowed := map[string]bool{"type": true, "command": true, "args": true}
	for k := range entry {
		if !allowed[k] {
			t.Errorf("key %q is not in Cherry Studio's strict schema — the import would fail whole", k)
		}
	}
	if entry["type"] != "stdio" {
		t.Errorf("type = %v, want stdio (one of the four literals the schema accepts)", entry["type"])
	}
}

// Cherry Studio's Claude agents run with CLAUDE_CONFIG_DIR at
// <userData>/Data/Agents/.claude, and a moved store (boot-config.json) moves
// it. Every Claude hook deja wires goes there, and uninstall takes them out.
func TestInstallCherryStudioAutoWritesClaudeHooksWhereTheAppPointsClaude(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	moved := filepath.Join(home, "cherry-data")
	if err := os.MkdirAll(filepath.Join(moved, "Data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moved, "Data", "cherrystudio.sqlite"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	boot, err := json.Marshal(map[string]any{"app.user_data_path": map[string]string{"/Applications/Cherry Studio.app": moved}})
	if err != nil {
		t.Fatal(err)
	}
	writeFileMkdir(t, filepath.Join(home, ".cherrystudio", "boot-config.json"), string(boot))

	if _, err := installCherryStudioAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(moved, "Data", "Agents", ".claude", "settings.json")
	var root struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(readFile(t, settings)), &root); err != nil {
		t.Fatalf("settings.json: %v", err)
	}
	for _, h := range claudeHookWiring {
		blocks := root.Hooks[h.Event]
		if len(blocks) == 0 || len(blocks[0].Hooks) == 0 || !strings.Contains(blocks[0].Hooks[0].Command, h.Sub) {
			t.Errorf("%s does not run %s: %+v", h.Event, h.Sub, blocks)
		}
	}
	if _, err := installCherryStudioAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(settings); err == nil && strings.Contains(string(b), "hook-context") {
		t.Errorf("uninstall left the hooks:\n%s", b)
	}
}
