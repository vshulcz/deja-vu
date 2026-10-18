package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// cline-auto writes the MCP entry first and the plugin second. With
// ~/.cline/plugins read-only the plugin was refused after the entry was
// already in cline_mcp_settings.json, so the target reported a refusal while
// half wired (#5203).
func TestClineAutoRefusesAnUnwritablePluginDirBeforeWriting(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions that bind the test user")
	}
	hermeticEnv(t)
	mcp := sources.ClineMCPSettingsPath()
	seed := []byte(`{"mcpServers": {"zz-mine": {"command": "my-server"}}}` + "\n")
	reseed := func(t *testing.T) {
		t.Helper()
		_ = os.RemoveAll(sources.ClinePluginsDir())
		if err := os.MkdirAll(filepath.Dir(mcp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(mcp, seed, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lock := func(t *testing.T, dirs ...string) {
		t.Helper()
		for _, d := range dirs {
			if err := os.Chmod(d, 0o555); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(d, 0o755) })
		}
	}
	pluginDir := func() string { return filepath.Join(sources.ClinePluginsDir(), "deja") }

	for _, tc := range []struct {
		name  string
		setup func(t *testing.T) []string
	}{
		{"plugins dir", func(t *testing.T) []string {
			if err := os.MkdirAll(sources.ClinePluginsDir(), 0o755); err != nil {
				t.Fatal(err)
			}
			return []string{sources.ClinePluginsDir()}
		}},
		// An older install already in place, its directory locked since: the
		// new plugin text has to go in and cannot.
		{"older plugin", func(t *testing.T) []string {
			if err := os.MkdirAll(pluginDir(), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pluginDir(), "index.js"), []byte("// an older deja plugin\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return []string{pluginDir()}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reseed(t)
			lock(t, tc.setup(t)...)
			_, err := captureRun(t, "install", "cline-auto", "--no-index")
			if err == nil {
				t.Fatal("install over a read-only plugin directory succeeded")
			}
			if !strings.Contains(err.Error(), "cline-auto") {
				t.Errorf("refusal does not name the target: %v", err)
			}
			if got, _ := os.ReadFile(mcp); !bytes.Equal(got, seed) {
				t.Errorf("cline_mcp_settings.json written before the plugin was refused:\n%s", got)
			}
		})
	}

	// Controls: a writable directory installs both halves, and a reinstall
	// that changes nothing in a locked plugin goes through — also when the
	// launcher is stale but its file writable, since it is rewritten in place.
	t.Run("writable", func(t *testing.T) {
		reseed(t)
		if _, err := captureRun(t, "install", "cline-auto", "--no-index"); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(mcp); bytes.Equal(got, seed) {
			t.Error("install left cline_mcp_settings.json without deja")
		}
		if _, err := os.Stat(filepath.Join(pluginDir(), "index.js")); err != nil {
			t.Errorf("install wrote no plugin: %v", err)
		}
	})
	t.Run("unchanged reinstall", func(t *testing.T) {
		reseed(t)
		if _, err := captureRun(t, "install", "cline-auto", "--no-index"); err != nil {
			t.Fatal(err)
		}
		launcher := dejaLauncherPath()
		if err := os.WriteFile(launcher, []byte("#!/bin/sh\n# an older launcher\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(mcp)
		skill := filepath.Join(pluginDir(), "skills", "deja-history")
		lock(t, skill, filepath.Dir(skill), pluginDir(), filepath.Dir(launcher))
		if _, err := captureRun(t, "install", "cline-auto", "--no-index"); err != nil {
			t.Fatalf("reinstall over an unchanged locked plugin refused: %v", err)
		}
		if got, _ := os.ReadFile(mcp); !bytes.Equal(got, before) {
			t.Errorf("reinstall changed cline_mcp_settings.json:\n%s", got)
		}
		if got, _ := os.ReadFile(launcher); bytes.Contains(got, []byte("an older launcher")) {
			t.Errorf("stale launcher not rewritten:\n%s", got)
		}
	})
}
