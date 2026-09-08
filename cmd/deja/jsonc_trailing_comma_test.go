package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// VS Code writes mcp.json with trailing commas; the JSONC probe stripped
// comments and then demanded strict JSON, so such a file refused the whole
// target (#3223).
func TestJSONCWithTrailingCommasIsJSONC(t *testing.T) {
	for _, s := range []string{
		"{\n \"a\": 1,\n}",
		"{\n \"a\": [1, 2,],\n}",
		"{ // c\n \"a\": {\"b\": 1,}, }\n",
	} {
		if !configIsJSONC([]byte(s)) {
			t.Errorf("not taken for JSONC: %q", s)
		}
	}
	for _, s := range []string{`{"a": 1}`, `{"a": ,}`, `{"a": 1,`, `{"a": "x,"}`} {
		if configIsJSONC([]byte(s)) {
			t.Errorf("taken for JSONC: %q", s)
		}
	}
}

func TestInstallVSCodeTakesAFileWithTrailingCommas(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "mcp.json")
	seed := "{\n\t\"servers\": {\n\t\t\"playwright\": {\n\t\t\t\"type\": \"stdio\",\n\t\t\t\"command\": \"npx\",\n\t\t\t\"args\": [\"@playwright/mcp@latest\"],\n\t\t},\n\t},\n}\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installVSCodeMCPAt(path, "/bin/deja", false); err != nil {
		t.Fatalf("install refused the editor's own file: %v", err)
	}
	b, _ := os.ReadFile(path)
	var root map[string]any
	if err := json.Unmarshal([]byte(stripTrailingCommas(stripJSONComments(string(b)))), &root); err != nil {
		t.Fatalf("the result is not JSONC any more: %v\n%s", err, b)
	}
	servers, _ := root["servers"].(map[string]any)
	if servers["deja"] == nil || servers["playwright"] == nil {
		t.Fatalf("servers = %v\n%s", servers, b)
	}
	if !strings.Contains(string(b), "@playwright/mcp@latest\"],") {
		t.Fatalf("the reader's own trailing comma did not survive:\n%s", b)
	}
	if _, err := installVSCodeMCPAt(path, "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != seed {
		t.Fatalf("uninstall did not give the file back:\n%s", got)
	}
}
