package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A config deja cannot parse is a config deja must not touch. The file belongs
// to the user — it may be mid-edit, or written by a version of the harness that
// deja does not know — and overwriting it to make room for a hook would destroy
// work to install a convenience.
func TestBrokenConfigIsLeftAlone(t *testing.T) {
	cases := []struct{ target, rel, broken string }{
		{"qwen-auto", ".qwen/settings.json", "{\"theme\": \"dark\",\n"},
		{"codex-auto", ".codex/hooks.json", "{ this is not json at all }"},
		{"grok-auto", ".grok/user-settings.json", "{\"apiKey\": "},
		{"opencode-auto", ".config/opencode/opencode.json", "{\"model\":"},
	}

	for _, tc := range cases {
		t.Run(tc.target, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))

			path := filepath.Join(home, filepath.FromSlash(tc.rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.broken), 0o644); err != nil {
				t.Fatal(err)
			}

			_, err := installTarget(tc.target, "/usr/local/bin/deja", false)
			after, rerr := os.ReadFile(path)
			if rerr != nil {
				t.Fatalf("the file is gone after a failed install: %v", rerr)
			}
			if string(after) != tc.broken {
				t.Fatalf("deja rewrote a config it could not read:\n--- was\n%s\n--- now\n%s",
					tc.broken, after)
			}
			// And it has to say so. Failing silently leaves someone believing
			// they have memory wired when they have not.
			if err == nil {
				t.Error("install reported success on a config it could not parse")
			}
		})
	}
}
