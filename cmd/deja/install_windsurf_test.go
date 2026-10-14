package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Cascade reads ~/.codeium/windsurf/mcp_config.json; the servers already in
// it stay, and uninstall takes only deja's (#308).
func TestInstallWindsurfKeepsTheOtherServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_WINDSURF_ROOT", "")
	path := filepath.Join(home, ".codeium", "windsurf", "mcp_config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"mcpServers": {"github": {"command": "gh-mcp", "args": []}}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := installWindsurf("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != path {
		t.Fatalf("path = %q, want %q", res.Path, path)
	}
	assertMCPServerEntry(t, path)
	servers := func() map[string]any {
		var doc struct {
			MCPServers map[string]any `json:"mcpServers"`
		}
		b, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(b, &doc) != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return doc.MCPServers
	}
	if _, ok := servers()["github"]; !ok {
		t.Fatal("install dropped the server that was already there")
	}
	if _, err := installWindsurf("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	got := servers()
	if _, ok := got["github"]; !ok || len(got) != 1 {
		t.Fatalf("after uninstall the servers are %v, want github alone", got)
	}
}

// A trajectory that fails authentication is a warning with a count, not a
// shorter history nobody mentions.
func TestDoctorCountsWindsurfTrajectoriesThatFailAuthentication(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "windsurf")
	t.Setenv("DEJA_WINDSURF_ROOT", root)
	if err := os.MkdirAll(filepath.Join(root, "cascade"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join("..", "..", "fixtures", "registry", "windsurf", "cascade")
	for i, name := range []string{"c4f3a2b1-7e6d-4c5b-9a8f-1e2d3c4b5a69", "8a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"} {
		b, err := os.ReadFile(filepath.Join(src, name+".pb"))
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			b[len(b)-1] ^= 0x01
		}
		if err := os.WriteFile(filepath.Join(root, "cascade", name+".pb"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := runDoctor(&out, nil, stubLookup("2.0.0", true), index.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "windsurf 1 trajectory failed authentication; those sessions are missing from recall") {
		t.Fatalf("doctor does not name the refused trajectory:\n%s", out.String())
	}
}
