package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Nobody installs into an empty home. They install into a config they have
// already set up, and the one thing deja must never do is take any of it away.
// Every case here seeds a file the way a person would have it, installs,
// uninstalls, and checks their content is still there both times.
func TestInstallPreservesWhatWasAlreadyThere(t *testing.T) {
	cases := []struct {
		target string
		rel    string // config path, relative to home
		before string
		keep   []string
	}{
		{"qwen-auto", ".qwen/settings.json",
			`{"theme":"dark","hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"/usr/bin/theirs"}]}]}}`,
			[]string{`"theme"`, "/usr/bin/theirs"}},
		{"kimi-auto", ".kimi-code/config.toml",
			"default_model = \"theirs\"\n\n[[hooks]]\nevent = \"SessionStart\"\ncommand = \"/usr/bin/theirs\"\n",
			[]string{`default_model = "theirs"`, "/usr/bin/theirs"}},
		{"goose-auto", ".config/goose/config.yaml",
			"GOOSE_PROVIDER: openai\nextensions:\n  theirs:\n    enabled: true\n    cmd: \"/usr/bin/theirs\"\n",
			[]string{"GOOSE_PROVIDER: openai", "theirs:", "/usr/bin/theirs"}},
		{"codex-auto", ".codex/hooks.json",
			`{"hooks":{"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"/usr/bin/theirs"}]}]}}`,
			[]string{"/usr/bin/theirs"}},
		{"grok-auto", ".grok/user-settings.json",
			`{"apiKey":"theirs","defaultModel":"grok-4"}`,
			[]string{`"apiKey"`, "grok-4"}},
		{"opencode-auto", ".config/opencode/opencode.json",
			`{"model":"theirs/model","mcp":{"theirs":{"type":"local","command":["/usr/bin/theirs"]}}}`,
			[]string{"theirs/model", "/usr/bin/theirs"}},
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
			if err := os.WriteFile(path, []byte(tc.before), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := installTarget(tc.target, "/usr/local/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			after := readFile(t, path)
			// The test means nothing if the install did not touch this file.
			if !strings.Contains(after, "/usr/local/bin/deja") {
				t.Fatalf("install wrote no deja wiring into %s; this case proves nothing:\n%s", tc.rel, after)
			}
			for _, want := range tc.keep {
				if !strings.Contains(after, want) {
					t.Errorf("install dropped %q from their config:\n%s", want, after)
				}
			}

			removingWiring = true
			_, uerr := installTarget(tc.target, "/usr/local/bin/deja", true)
			removingWiring = false
			if uerr != nil {
				t.Fatalf("uninstall: %v", uerr)
			}
			restored := readFile(t, path)
			for _, want := range tc.keep {
				if !strings.Contains(restored, want) {
					t.Errorf("uninstall dropped %q from their config:\n%s", want, restored)
				}
			}
			if strings.Contains(restored, "/usr/local/bin/deja") {
				t.Errorf("uninstall left deja behind in their config:\n%s", restored)
			}
		})
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}
