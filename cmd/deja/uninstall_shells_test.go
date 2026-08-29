package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #840 taught uninstall to delete a config that turned out to be entirely
// deja's: "removing something must not leave more behind than it found". The
// test for that is byte-emptiness, which only the markdown and TOML writers
// reach — every structured writer leaves `{"mcpServers":{}}` instead. So
// installing the whole family and uninstalling it leaves sixteen shells of
// files deja created itself (#2583).
func TestUninstallRemovesTheShellsItCreated(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if !strings.Contains(home, "TestUninstallRemovesTheShells") {
		t.Fatalf("refusing to install into %q", home)
	}
	targets := []string{"claude-code", "cursor", "gemini", "kimi", "qwen", "copilot", "pi", "omp"}
	for _, target := range targets {
		if _, err := captureRun(t, "install", target, "--no-index", "--no-guidance"); err != nil {
			t.Fatalf("install %s: %v", target, err)
		}
	}
	// The premise: those installs created files that were not there before.
	if _, err := os.Stat(filepath.Join(home, ".cursor", "mcp.json")); err != nil {
		t.Fatalf("install wrote no config: %v", err)
	}
	for _, target := range targets {
		if _, err := captureRun(t, "uninstall", target); err != nil {
			t.Fatalf("uninstall %s: %v", target, err)
		}
	}
	var left []string
	_ = filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		// deja's own state is not a harness config.
		if strings.Contains(p, filepath.Join(".config", "deja")) {
			return nil
		}
		left = append(left, strings.TrimPrefix(p, home))
		return nil
	})
	if len(left) > 0 {
		t.Errorf("uninstall left %d files deja created:\n  %s", len(left), strings.Join(left, "\n  "))
	}
}

// A config the reader already had keeps its place, emptied of deja and nothing
// else — deleting that one would be taking away a file deja never made.
func TestUninstallKeepsAConfigItDidNotCreate(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	path := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{\n  \"mcpServers\": {}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "cursor", "--no-index", "--no-guidance"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "cursor"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("a config deja did not create was removed: %v", err)
	}
}
