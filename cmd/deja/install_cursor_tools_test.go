package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Cursor reads ~/.claude/settings.json as well as its own hooks.json and maps
// the Claude event names onto its own — PreToolUse→preToolUse,
// PostToolUse→postToolUse, PreCompact→preCompact — so it has been running these
// three deja hooks all along for anyone who also has Claude Code installed, and
// none of them for anyone who does not. They are also three of the five events
// cursor lists as taking a hook's additionalContext.
func TestInstallCursorWiresTheMomentOfTheAction(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installCursorHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".cursor", "hooks.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks map[string][]struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	for event, sub := range map[string]string{
		"sessionStart":       "hook-context",
		"beforeSubmitPrompt": "hook-prompt",
		"preToolUse":         "hook-tool",
		"postToolUse":        "hook-tool-after",
		"preCompact":         "hook-precompact",
	} {
		entries := root.Hooks[event]
		if len(entries) != 1 {
			t.Fatalf("%s entries = %d, want 1: %s", event, len(entries), b)
		}
		if got := entries[0].Command; !strings.HasSuffix(got, sub) {
			t.Errorf("%s runs %q, want %s", event, got, sub)
		}
	}
	// The command string is what cursor dedupes on against ~/.claude, so it has
	// to be exactly what deja writes there — a wrapper or a flag here would
	// give a user with both two hooks per action.
	if got := root.Hooks["postToolUse"][0].Command; got != "/usr/local/bin/deja hook-tool-after" {
		t.Errorf("command = %q, want the same string the claude wiring writes", got)
	}
	// And uninstall takes all five back out.
	if _, err := installCursorHooks("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	b2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b2), "deja hook-") {
		t.Errorf("uninstall left a hook behind: %s", b2)
	}
}
