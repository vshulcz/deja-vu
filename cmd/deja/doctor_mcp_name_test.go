package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Reported from a real setup: the server registered by hand as "deja-vu" for
// three releases, answering calls daily, and doctor called it not wired. The
// first thing someone runs when memory goes quiet must not send them looking
// in the wrong place.
func TestDoctorFindsTheServerUnderAnyName(t *testing.T) {
	dir := t.TempDir()
	probe := doctorJSONWired("mcpServers")
	cases := map[string]string{
		"named deja-vu":    `{"mcpServers":{"deja-vu":{"command":"/usr/local/bin/deja","args":["mcp"]}}}`,
		"named memory":     `{"mcpServers":{"memory":{"command":"deja","args":["mcp"]}}}`,
		"windows cmd shim": `{"mcpServers":{"deja-vu":{"command":"cmd","args":["/c","deja","mcp"]}}}`,
		"windows exe":      `{"mcpServers":{"dj":{"command":"C:\\tools\\deja.exe","args":["mcp"]}}}`,
		"nested transport": `{"mcpServers":{"whatever":{"transport":{"type":"stdio","command":"/opt/deja","args":["mcp"]}}}}`,
		"canonical name":   `{"mcpServers":{"deja":{"command":"/usr/local/bin/deja","args":["mcp"]}}}`,
	}
	for name, body := range cases {
		p := filepath.Join(dir, name+".json")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if !probe(p) {
			t.Fatalf("%s: reported not wired", name)
		}
	}
	// A config with someone else's server must still read as not wired, or
	// the check tells everyone they are fine.
	other := filepath.Join(dir, "other.json")
	if err := os.WriteFile(other, []byte(`{"mcpServers":{"memory":{"command":"npx","args":["-y","@modelcontextprotocol/server-memory"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if probe(other) {
		t.Fatal("an unrelated MCP server was reported as deja")
	}
}

func TestDoctorTOMLFindsTheServerUnderAnyName(t *testing.T) {
	dir := t.TempDir()
	renamed := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(renamed, []byte("[mcp_servers.deja-vu]\ncommand = \"/usr/local/bin/deja\"\nargs = [\"mcp\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !doctorTOMLWired(renamed) {
		t.Fatal("renamed toml server reported not wired")
	}
	foreign := filepath.Join(dir, "foreign.toml")
	if err := os.WriteFile(foreign, []byte("[mcp_servers.memory]\ncommand = \"npx\"\nargs = [\"-y\",\"server-memory\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if doctorTOMLWired(foreign) {
		t.Fatal("an unrelated toml server was reported as deja")
	}
}
