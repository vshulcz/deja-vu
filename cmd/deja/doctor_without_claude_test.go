package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// doctor is what someone runs when memory is not working, so it must not go
// quiet on the harnesses they actually have. It read Claude Code's settings
// first and returned early when that file was missing — which is most machines
// — and with it went the wiring table for every other harness: a correctly
// wired qwen showed up nowhere at all.
func TestDoctorShowsEveryHarnessWithoutClaudeCode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))

	if _, err := installTarget("qwen-auto", "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); err == nil {
		t.Fatal("this home was supposed to have no Claude Code config")
	}

	var out bytes.Buffer
	doctorHooks(&out)
	got := out.String()

	if !strings.Contains(got, "qwen") {
		t.Fatalf("doctor said nothing about qwen:\n%s", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "qwen") && !strings.Contains(line, "wired") {
			t.Errorf("a wired qwen is reported as %q", strings.TrimSpace(line))
		}
	}
	// And the rest of the table is there to compare against.
	for _, name := range []string{"opencode", "gemini", "goose", "cline"} {
		if !strings.Contains(got, name) {
			t.Errorf("doctor lists no row for %s:\n%s", name, got)
		}
	}
}
