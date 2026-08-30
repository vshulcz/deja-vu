package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Install writes into files people also edit. Replacing a deja entry someone
// pointed at their own wrapper is the right thing to do; doing it in the same
// sentence deja prints for a config that never mentioned it is not (#2390).
func TestInstallSaysWhenItReplacedAnEntrySomeoneChanged(t *testing.T) {
	cases := []struct {
		name   string
		before string
		want   string // "" means: say nothing besides the action
	}{
		{"a config that never mentioned deja",
			`{"mcpServers":{"mine":{"command":"/usr/local/bin/my-server"}}}`, ""},
		{"the entry deja itself wrote",
			`{"mcpServers":{"deja":{"type":"stdio","command":"/usr/local/bin/deja","args":["mcp"]}}}`, ""},
		{"an entry someone pointed at their own wrapper",
			`{"mcpServers":{"deja":{"command":"/home/me/bin/deja-wrapper","args":["mcp","--quiet"]}}}`,
			"/home/me/bin/deja-wrapper"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))
			if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(tc.before), 0o644); err != nil {
				t.Fatal(err)
			}

			r, err := installTarget("claude", "/usr/local/bin/deja", false)
			if err != nil {
				t.Fatalf("install: %v", err)
			}
			if tc.want == "" {
				if r.Note != "" {
					t.Errorf("install spoke about an entry nobody had changed: %q", r.Note)
				}
				return
			}
			if !strings.Contains(r.Note, tc.want) {
				t.Errorf("install replaced their wiring without naming it: note %q", r.Note)
			}
		})
	}
}
