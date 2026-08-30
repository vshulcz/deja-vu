package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A target that wires more than one file wrote the first and refused the
// second, so a run reported as refused had already changed a config and left a
// snapshot beside it. What a reader is told is "refused"; what is on disk is
// half an install (#2744).
func TestATargetThatWillRefuseWritesNothing(t *testing.T) {
	for _, c := range []struct {
		target string
		// the file the target cannot read, and the one it would have written
		// first
		unreadable func() string
		written    func() string
	}{
		{"cursor-auto",
			func() string { return filepath.Join(sources.CursorCLIHome(), "hooks.json") },
			func() string { return filepath.Join(sources.CursorCLIHome(), "mcp.json") }},
		{"claude-auto",
			func() string { return filepath.Join(sources.ClaudeConfigDir(), "settings.json") },
			func() string { return sources.ClaudeJSONPath() }},
		{"codex-auto",
			func() string { return filepath.Join(sources.CodexHome(), "hooks.json") },
			func() string { return filepath.Join(sources.CodexHome(), "config.toml") }},
		{"grok-auto",
			func() string { return filepath.Join(sources.GrokHome(), "user-settings.json") },
			func() string { return filepath.Join(sources.GrokHome(), "config.toml") }},
	} {
		t.Run(c.target, func(t *testing.T) {
			hermeticEnv(t)
			bad, first := c.unreadable(), c.written()
			for _, dir := range []string{filepath.Dir(bad), filepath.Dir(first)} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			// A file the hook writers cannot edit: comments are the ordinary
			// way to get there, and #2739 deliberately does not help here.
			if err := os.WriteFile(bad, []byte("{\n  // mine\n  \"hooks\": {}\n}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			before := "{\n  \"mcpServers\": {}\n}\n"
			if strings.HasSuffix(first, ".toml") {
				before = "[tools]\nweb = true\n"
			}
			if err := os.WriteFile(first, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := captureRun(t, "install", c.target, "--no-index"); err == nil {
				t.Fatal("a target that cannot finish reported itself installed")
			}
			b, err := os.ReadFile(first)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != before {
				t.Errorf("the first config was written before the refusal:\n%s", b)
			}
			if _, err := os.Stat(first + ".bak"); err == nil {
				t.Errorf("a snapshot was left beside a file that was never changed")
			}
		})
	}
}

// And a target whose files are all readable still installs.
func TestThePreflightDoesNotRefuseAReadableConfig(t *testing.T) {
	hermeticEnv(t)
	hooks := filepath.Join(sources.CursorCLIHome(), "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooks), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hooks, []byte(`{"version":1,"hooks":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "cursor-auto", "--no-index"); err != nil {
		t.Fatalf("a readable config was refused: %v", err)
	}
	b, err := os.ReadFile(hooks)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "hook-context") {
		t.Errorf("the hook was not written:\n%s", b)
	}
}

// The check itself: a file that is not there, an empty one and a readable one
// are all fine, and only a broken one is named.
func TestReadableStrictJSONNamesOnlyWhatItCannotRead(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.json")
	empty := filepath.Join(dir, "empty.json")
	good := filepath.Join(dir, "good.json")
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(empty, []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(good, []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("{\n // c\n}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := readableStrictJSON(missing, empty, good); err != nil {
		t.Errorf("a readable set was refused: %v", err)
	}
	err := readableStrictJSON(good, bad, missing)
	if err == nil {
		t.Fatal("a file that cannot be parsed was accepted")
	}
	if !strings.Contains(err.Error(), bad) {
		t.Errorf("the refusal does not name the file: %v", err)
	}
}
