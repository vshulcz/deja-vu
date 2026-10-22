package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A CRLF config with a comment in it goes through the JSONC text writers, which
// looked for '\n' only: the second opencode install put a comma after the CR
// (`},\r,`) and opencode refused the file, and the others left a `   \r` line
// that uninstall kept (#4553).
func TestCRLFCommentedConfigsRoundTrip(t *testing.T) {
	const exe = "/usr/local/bin/deja"
	for _, c := range []struct {
		name  string
		seed  string
		path  func(dir string) string
		write func(path string, uninstall bool) (installResult, error)
	}{
		{"opencode", "{\n  // my servers\n  \"mcp\": {\n    \"mine\": {\"type\": \"local\", \"command\": [\"my-server\"]}\n  }\n}\n",
			func(dir string) string { return filepath.Join(dir, "opencode.json") },
			func(p string, u bool) (installResult, error) {
				return installOpencodeShaped(filepath.Dir(p), "opencode", exe, u)
			}},
		{"gemini", "{\n  // mine\n  \"mcpServers\": {\n    \"mine\": {\"command\": \"my-server\"}\n  }\n}\n",
			func(dir string) string { return filepath.Join(dir, "settings.json") },
			func(p string, u bool) (installResult, error) { return installMCPJSON(p, exe, u) }},
		{"vscode", "{\n  // mine\n  \"servers\": {\n    \"mine\": {\"type\": \"stdio\", \"command\": \"my-server\"}\n  }\n}\n",
			func(dir string) string { return filepath.Join(dir, "mcp.json") },
			func(p string, u bool) (installResult, error) { return installVSCodeMCPAt(p, exe, u) }},
		{"prime", "{\n  // mine\n  \"mcpServers\": {\n    \"mine\": {\"command\": \"my-server\"}\n  }\n}\n",
			func(dir string) string { return filepath.Join(dir, "settings.json") },
			func(p string, u bool) (installResult, error) { return installPrimeMCPAt(p, exe, u) }},
		{"amp", "{\n  // mine\n  \"amp.mcpServers\": {\n    \"mine\": {\"command\": \"my-server\"}\n  }\n}\n",
			func(dir string) string { return filepath.Join(dir, "settings.json") },
			func(p string, u bool) (installResult, error) { return installAmpMCPAt(p, exe, u) }},
		{"openclaw", "{\n  // mine\n  \"mcp\": {\n    \"servers\": {\n      \"mine\": {\"command\": \"my-server\"}\n    }\n  }\n}\n",
			func(string) string { return filepath.Join(sources.OpenClawStateDir(), "openclaw.json") },
			func(_ string, u bool) (installResult, error) { return installOpenClawMCP(exe, u) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			t.Cleanup(func() { removingWiring = false })
			path := c.path(t.TempDir())
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			seed := strings.ReplaceAll(c.seed, "\n", "\r\n")
			if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
				t.Fatal(err)
			}
			for i := range 2 {
				if _, err := c.write(path, false); err != nil {
					t.Fatalf("install %d: %v", i+1, err)
				}
			}
			b, _ := os.ReadFile(path)
			var probe any
			if err := json.Unmarshal([]byte(jsoncToJSON(string(b))), &probe); err != nil {
				t.Fatalf("after two installs the config is not JSONC (%v):\n%q", err, b)
			}
			for i, line := range strings.SplitAfter(string(b), "\n") {
				if strings.TrimSpace(line) == "" && line != "" {
					t.Fatalf("line %d is blank in what install wrote:\n%q", i+1, b)
				}
			}
			removingWiring = true
			if _, err := c.write(path, true); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			if got, _ := os.ReadFile(path); string(got) != seed {
				t.Fatalf("uninstall did not give the file back:\n%q\nwant\n%q", got, seed)
			}
		})
	}
}
