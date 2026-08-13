package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A config written on Windows, or by an editor set that way, uses CRLF. deja
// edits several of them as text, splitting on "\n" — which leaves a stray "\r"
// on the end of every line it inspects. The risk is not cosmetic: a key
// compared against "extensions:" never matches "extensions:\r", so deja adds a
// second one and the file stops parsing.
func TestCRLFConfigSurvivesInstall(t *testing.T) {
	for _, tc := range []struct {
		target, rel, before string
		keep                []string
	}{
		{"goose-auto", ".config/goose/config.yaml",
			"GOOSE_PROVIDER: openai\r\nextensions:\r\n  theirs:\r\n    enabled: true\r\n",
			[]string{"GOOSE_PROVIDER: openai", "theirs:"}},
		{"kimi-auto", ".kimi-code/config.toml",
			"default_model = \"theirs\"\r\n\r\n[providers.theirs]\r\ntype = \"openai\"\r\n",
			[]string{`default_model = "theirs"`, "[providers.theirs]"}},
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
			if err := os.WriteFile(path, []byte(tc.before), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := installTarget(tc.target, "/usr/local/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			after := readFile(t, path)
			for _, want := range tc.keep {
				if !strings.Contains(after, want) {
					t.Errorf("install lost %q from a CRLF config:\n%q", want, after)
				}
			}
			if !strings.Contains(after, "/usr/local/bin/deja") {
				t.Fatalf("install wrote nothing into a CRLF config:\n%q", after)
			}
			// The shape that breaks the file: a key deja failed to recognise
			// and therefore wrote again.
			for _, key := range []string{"extensions:", "[[hooks]]"} {
				if n := strings.Count(after, key); n > 1 {
					t.Errorf("%q appears %d times after install:\n%q", key, n, after)
				}
			}
		})
	}
}
