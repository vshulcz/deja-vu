package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// opencode's config is JSONC whatever it is called: the text writer was picked
// by the file's extension, so the same content in `opencode.json` was decoded
// strictly and the target refused — the reader who annotated their config
// could not install deja at all (#1664, the half #1663 left).
func TestOpencodeReadsTheContentNotTheExtension(t *testing.T) {
	for _, name := range []string{"opencode.json", "opencode.jsonc"} {
		t.Run(name, func(t *testing.T) {
			hermeticEnv(t)
			dir := filepath.Join(opencodeConfigHome(), "opencode")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, name)
			before := "{\n  // the theme is deliberate\n  \"theme\": \"opencode\",\n  \"mcp\": {}\n}\n"
			if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := captureRun(t, "install", "opencode", "--no-index"); err != nil {
				t.Fatalf("a commented config was refused: %v", err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), "// the theme is deliberate") {
				t.Errorf("the comment was dropped:\n%s", b)
			}
			if !strings.Contains(string(b), `"deja"`) {
				t.Errorf("the entry was not written:\n%s", b)
			}

			// And out again, leaving the reader's file as it was.
			if _, err := captureRun(t, "uninstall", "opencode"); err != nil {
				t.Fatal(err)
			}
			b, err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), `"deja"`) {
				t.Errorf("the entry survived the uninstall:\n%s", b)
			}
			if !strings.Contains(string(b), "// the theme is deliberate") {
				t.Errorf("the comment was dropped on the way out:\n%s", b)
			}
		})
	}
}

// A block written inline has no end line to find, so the entry landed after
// its closing brace and the config stopped parsing — with install reporting
// success (#2777). An empty inline block is what a config nobody has added a
// server to looks like.
func TestOpencodeOpensAnInlineBlockRatherThanWritingPastIt(t *testing.T) {
	for _, before := range []string{
		"{\n  \"theme\": \"opencode\",\n  \"mcp\": {}\n}\n",
		"{\n  \"mcp\": {\"other\": {\"type\":\"local\",\"command\":[\"x\"]}}\n}\n",
		"{\n  // annotated\n  \"mcp\": {}\n}\n",
	} {
		t.Run(strings.TrimSpace(strings.SplitN(before, "\n", 3)[1]), func(t *testing.T) {
			hermeticEnv(t)
			dir := filepath.Join(opencodeConfigHome(), "opencode")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "opencode.jsonc")
			if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "install", "opencode", "--no-index"); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var probe map[string]any
			if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &probe); err != nil {
				t.Fatalf("the config no longer parses: %v\n%s", err, b)
			}
			mcp, _ := probe["mcp"].(map[string]any)
			if _, ok := mcp["deja"]; !ok {
				t.Errorf("the entry is not in the block:\n%s", b)
			}
			if strings.Contains(before, "other") {
				if _, ok := mcp["other"]; !ok {
					t.Errorf("the reader's own server was dropped:\n%s", b)
				}
			}
		})
	}
}
