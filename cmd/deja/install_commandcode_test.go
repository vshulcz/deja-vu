package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// All four of Command Code's surfaces are shapes deja already writes, and the
// two that would fail quietly are the ones this pins: the hook timeout is in
// seconds there, not milliseconds, and its tool matcher uses its own display
// names, so a Claude-shaped `Bash` would never fire (#3651).
func TestInstallCommandCodeWritesAllFourSurfaces(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if _, err := installCommandCodeAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(home, ".commandcode")
	assertMCPServerEntry(t, filepath.Join(root, "mcp.json"))

	if b, err := os.ReadFile(filepath.Join(root, "skills", "deja-search", "SKILL.md")); err != nil {
		t.Fatalf("skill: %v", err)
	} else if !strings.Contains(string(b), "deja") {
		t.Errorf("the skill does not mention deja")
	}
	if b, err := os.ReadFile(filepath.Join(root, "commands", "deja.md")); err != nil {
		t.Fatalf("command: %v", err)
	} else if !strings.Contains(string(b), "deja") {
		t.Errorf("the command does not mention deja")
	}

	b, err := os.ReadFile(filepath.Join(root, "settings.json"))
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	var cfg struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("settings are not the shape Command Code reads: %v", err)
	}
	for _, event := range []string{"SessionStart", "PreToolUse", "PostToolUse"} {
		entries := cfg.Hooks[event]
		if len(entries) == 0 || len(entries[0].Hooks) == 0 {
			t.Fatalf("%s has no hook", event)
		}
		h := entries[0].Hooks[0]
		if !strings.Contains(h.Command, "deja") {
			t.Errorf("%s runs %q", event, h.Command)
		}
		// Seconds, not milliseconds: 60000 here would be ten minutes, well
		// past the documented 600-second maximum.
		if h.Timeout > 600 {
			t.Errorf("%s timeout = %d, and Command Code reads that as seconds", event, h.Timeout)
		}
	}
	// Its tool names are SHELL/EDIT/WRITE/READ, so a Bash matcher fires never.
	if m := cfg.Hooks["PreToolUse"][0].Matcher; strings.Contains(m, "Bash") || !strings.Contains(m, "SHELL") {
		t.Errorf("PreToolUse matcher = %q, want Command Code's own tool names", m)
	}

	if _, err := installCommandCodeAuto("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "settings.json")); err == nil && strings.Contains(string(b), "deja") {
		t.Errorf("a hook survived uninstall: %s", b)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "deja-search", "SKILL.md")); err == nil {
		t.Error("the skill survived uninstall")
	}
}
