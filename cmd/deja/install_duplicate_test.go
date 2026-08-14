package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A config can end up with deja's entry twice: a hand-edited copy, a merge
// conflict resolved by keeping both sides, an upgrade that half-finished.
// Uninstalling has to take out every one of them — leaving a single copy means
// the harness goes on calling a binary the user removed, which is the failure
// they came to uninstall to avoid.
func TestUninstallRemovesEveryCopyOfItself(t *testing.T) {
	for _, tc := range []struct{ target, rel, doubled string }{
		{"qwen-auto", ".qwen/settings.json",
			`{"hooks":{"UserPromptSubmit":[` +
				`{"hooks":[{"type":"command","command":"/usr/local/bin/deja hook-prompt","timeout":60000}]},` +
				`{"hooks":[{"type":"command","command":"/usr/local/bin/deja hook-prompt","timeout":60000}]}` +
				`]}}`},
		{"codex-auto", ".codex/hooks.json",
			`{"hooks":{"SessionStart":[` +
				`{"matcher":"startup|resume","hooks":[{"type":"command","command":"/usr/local/bin/deja hook-context","timeout":10}]},` +
				`{"matcher":"startup|resume","hooks":[{"type":"command","command":"/usr/local/bin/deja hook-context","timeout":10}]}` +
				`]}}`},
	} {
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
			if err := os.WriteFile(path, []byte(tc.doubled), 0o644); err != nil {
				t.Fatal(err)
			}

			removingWiring = true
			_, err := installTarget(tc.target, "/usr/local/bin/deja", true)
			removingWiring = false
			if err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			left, rerr := os.ReadFile(path)
			if rerr != nil {
				return // the whole file was ours and went with it
			}
			if strings.Contains(string(left), "/usr/local/bin/deja") {
				t.Fatalf("uninstall left a copy of deja behind:\n%s", left)
			}
		})
	}

	// And installing onto a doubled config must collapse it rather than add a
	// third.
	t.Run("install-collapses", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))
		path := filepath.Join(home, ".qwen", "settings.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		doubled := `{"hooks":{"UserPromptSubmit":[` +
			`{"hooks":[{"type":"command","command":"/usr/local/bin/deja hook-prompt","timeout":60000}]},` +
			`{"hooks":[{"type":"command","command":"/usr/local/bin/deja hook-prompt","timeout":60000}]}` +
			`]}}`
		if err := os.WriteFile(path, []byte(doubled), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := installTarget("qwen-auto", "/usr/local/bin/deja", false); err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(readFile(t, path), "hook-prompt"); n != 1 {
			t.Fatalf("install left %d copies of the hook:\n%s", n, readFile(t, path))
		}
	})
}
