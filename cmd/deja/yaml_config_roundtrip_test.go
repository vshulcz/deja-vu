package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #2604 gave the JSON writers the rule: a container deja added goes with deja,
// one the reader wrote stays. Sweeping the rest of the family, the two YAML
// writers still fail their own version of it — hermes leaves the `mcp_servers:`
// block it added in a config the reader owned, and goose returns the file
// without the trailing newline it came with (#2606).
func TestAYamlConfigComesBackAsItWas(t *testing.T) {
	// The path each writer actually uses, asked rather than restated: goose is
	// one of the few that does not keep its config under ~/.config on Windows,
	// so a hard-coded relative path was a config deja never reads (#2808).
	for _, tc := range []struct {
		name    string
		target  string
		path    func() string
		content string
	}{
		{"hermes", "hermes", func() string {
			return filepath.Join(os.Getenv("HOME"), ".hermes", "config.yaml")
		}, "profile: default\n"},
		{"goose", "goose", func() string {
			return filepath.Join(gooseConfigDir(), "config.yaml")
		}, "GOOSE_MODEL: gpt-5\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			path := tc.path()
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "install", tc.target, "--no-index", "--no-guidance"); err != nil {
				t.Fatal(err)
			}
			// The premise: deja really wired itself into that file.
			if b, err := os.ReadFile(path); err != nil || !strings.Contains(string(b), "deja") {
				t.Fatalf("install wired nothing here: %v", err)
			}
			if _, err := captureRun(t, "uninstall", tc.target); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("the reader's config is gone: %v", err)
			}
			if string(b) != tc.content {
				t.Errorf("the config did not come back as it was:\nwant %q\ngot  %q", tc.content, b)
			}
		})
	}
}

// A container the reader wrote survives, entries and all.
func TestAYamlConfigKeepsTheBlockTheReaderWrote(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	path := filepath.Join(home, ".hermes", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "profile: default\n\nmcp_servers:\n  theirs:\n    command: \"x\"\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "hermes", "--no-index", "--no-guidance"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "hermes"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "theirs") || !strings.Contains(string(b), "mcp_servers:") {
		t.Errorf("the reader's own block did not survive:\n%s", b)
	}
}
