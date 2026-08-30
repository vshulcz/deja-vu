package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The JSON writers round-trip through map[string]any and MarshalIndent, so an
// install rewrote the whole document: a four-space file came back at two and
// the top-level keys came back in alphabetical order (#2640).
func TestInstallKeepsTheShapeOfAJSONConfig(t *testing.T) {
	for _, tc := range []struct {
		name, target, rel, body string
		wants                   []string
	}{
		{
			name:   "four-space indent",
			target: "cursor",
			rel:    ".cursor/mcp.json",
			body:   "{\n    \"mcpServers\": {\n        \"theirs\": {\n            \"command\": \"x\"\n        }\n    }\n}\n",
			wants:  []string{"\n    \"mcpServers\""},
		},
		{
			name:   "the order they wrote",
			target: "cursor",
			rel:    ".cursor/mcp.json",
			body:   "{\n  \"zzz\": 1,\n  \"mcpServers\": {\n    \"theirs\": {\n      \"command\": \"x\"\n    }\n  },\n  \"aaa\": 2\n}\n",
			wants:  []string{"\"zzz\""},
		},
		{
			name:   "a tab-indented file",
			target: "cursor",
			rel:    ".cursor/mcp.json",
			body:   "{\n\t\"mcpServers\": {\n\t\t\"theirs\": {\n\t\t\t\"command\": \"x\"\n\t\t}\n\t}\n}\n",
			wants:  []string{"\n\t\"mcpServers\""},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			path := filepath.Join(home, filepath.FromSlash(tc.rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := installTarget(tc.target, "/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got := string(b)
			if !strings.Contains(got, `"deja"`) {
				t.Fatalf("install wrote no entry:\n%s", got)
			}
			for _, want := range tc.wants {
				if !strings.Contains(got, want) {
					t.Fatalf("the file came back in a shape the reader did not write (%q missing):\n%s", want, got)
				}
			}
			// The order they wrote, for the keys that were already there.
			if tc.name == "the order they wrote" {
				zzz, aaa := strings.Index(got, `"zzz"`), strings.Index(got, `"aaa"`)
				if zzz < 0 || aaa < 0 || zzz > aaa {
					t.Fatalf("the top-level keys were reordered:\n%s", got)
				}
			}
		})
	}
}

// A config deja creates has no shape to keep, so it gets the house style.
func TestInstallWritesTwoSpacesIntoAConfigItCreated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if _, err := installTarget("cursor", "/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".cursor", "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "\n  \"mcpServers\"") {
		t.Fatalf("a config deja wrote itself is not in the house style:\n%s", b)
	}
}
