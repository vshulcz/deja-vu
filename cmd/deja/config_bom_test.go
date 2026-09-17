package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PowerShell 5.1 writes UTF-8 with a byte order mark by default, and editors
// on Windows do too, so a config that has been through one refused every JSON
// target deja has — with a remedy naming a character nobody can see (#3696).
// The mark belongs with the line endings and the indent: read past it, and put
// it back on what is written.
func TestAConfigWithAByteOrderMarkIsStillInstallable(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	seed := func(rel, body string) string {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, append(append([]byte(nil), utf8BOM...), body...), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	files := map[string]string{
		".claude.json":           `{"editorMode":"vim"}`,
		".claude/settings.json":  `{"model":"opus"}`,
		".cursor/mcp.json":       `{"mcpServers":{}}`,
		".gemini/settings.json":  `{"theme":"dark"}`,
		".qwen/settings.json":    `{"theme":"dark"}`,
		".zcode/cli/config.json": `{}`,
	}
	paths := map[string]string{}
	for rel, body := range files {
		paths[rel] = seed(rel, body)
	}
	for _, target := range []string{"claude-code", "claude-auto", "cursor", "gemini", "gemini-auto", "qwen", "qwen-auto", "zcode", "zcode-auto"} {
		out, err := captureRun(t, "install", target, "--no-index")
		if err != nil {
			t.Errorf("install %s refused a config with a byte order mark: %v", target, err)
			continue
		}
		if strings.Contains(out, "refused") {
			t.Errorf("install %s refused a config with a byte order mark:\n%s", target, out)
		}
	}
	for rel, keptKey := range map[string]string{
		".claude.json":          "editorMode",
		".claude/settings.json": "model",
		".gemini/settings.json": "theme",
		".qwen/settings.json":   "theme",
	} {
		b, err := os.ReadFile(paths[rel])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(b, utf8BOM) {
			t.Errorf("%s came back without the mark it had", rel)
		}
		var root map[string]any
		if err := json.Unmarshal(bytes.TrimPrefix(b, utf8BOM), &root); err != nil {
			t.Fatalf("%s is not readable after an install: %v", rel, err)
		}
		if _, ok := root[keptKey]; !ok {
			t.Errorf("%s lost the reader's own %q", rel, keptKey)
		}
		if !mentionsDeja(b) {
			t.Errorf("%s carries no wiring of deja's after an install: %s", rel, b)
		}
	}
}

// And a mark is not added to a file that had none: the file keeps the shape its
// owner's tools give it, in both directions.
func TestNoByteOrderMarkIsAddedToAConfigWithoutOne(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(p, []byte("{\n  \"editorMode\": \"vim\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-code", "--no-index"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(b, utf8BOM) {
		t.Errorf("install put a byte order mark on a file that had none")
	}
}
