package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The .json writer names the entry it replaced (#2390). Opencode's second
// writer edits lines so comments survive, and said nothing — so what install
// told you depended on which of the two names your config had (#2392).
func TestInstallSaysWhatItReplacedInAJSONCConfig(t *testing.T) {
	cases := []struct {
		name   string
		before string
		want   string // "" means: say nothing besides the action
	}{
		{"an entry someone pointed at their own wrapper", `{
  // my settings
  "mcp": {
    "mine": {"type":"local","command":["/usr/bin/mine"]},
    "deja": {"type":"local","command":["/home/me/bin/deja-wrapper","mcp","--quiet"]}
  }
}
`, "/home/me/bin/deja-wrapper"},
		{"no deja entry at all", `{
  "mcp": {
    "mine": {"type":"local","command":["/usr/bin/mine"]}
  }
}
`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))
			dir := filepath.Join(home, ".config", "opencode")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "opencode.jsonc")
			if err := os.WriteFile(path, []byte(tc.before), 0o644); err != nil {
				t.Fatal(err)
			}

			r, err := installTarget("opencode", "/usr/local/bin/deja", false)
			if err != nil {
				t.Fatalf("install: %v", err)
			}
			if tc.want == "" {
				if r.Note != "" {
					t.Errorf("install spoke about an entry nobody had: %q", r.Note)
				}
				return
			}
			if !strings.Contains(r.Note, tc.want) {
				t.Errorf("install replaced their wiring without naming it: note %q", r.Note)
			}
			// The comment is why this writer exists at all.
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "// my settings") {
				t.Errorf("the comment did not survive the write:\n%s", body)
			}
		})
	}
}
