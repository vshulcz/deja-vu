package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A settings.json with a comment was re-marshalled whole on install, and the
// comment was gone for good; the JSONC path keeps every other byte.
func TestInstallPrimeKeepsTheComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	seed := "{\n  // keep me\n  \"mcpServers\": {\n    \"fs\": {\"type\": \"stdio\", \"command\": \"fs-mcp\", \"args\": []}\n  }\n}\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installPrimeMCPAt(path, "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "// keep me") || !strings.Contains(string(b), `"deja"`) || !strings.Contains(string(b), `"fs"`) {
		t.Fatalf("install lost the comment or a server:\n%s", b)
	}
	if _, err := installPrimeMCPAt(path, "/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != seed {
		t.Fatalf("uninstall did not give the file back:\n%s", got)
	}
}
