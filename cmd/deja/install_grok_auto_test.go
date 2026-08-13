package main

import (
	"encoding/json"
	"os"
	"testing"
)

// Grok's documented hook events are the same four deja wires for Claude Code,
// and it reads them from a file in ~/.grok/hooks. Getting the shape wrong is
// silent: the file loads, nothing fires, and recall simply never appears.
func TestGrokAutoWritesEveryHookEvent(t *testing.T) {
	hermeticEnv(t)
	r, err := installGrokAuto("/bin/deja", false)
	if err != nil || r.Action != "created" {
		t.Fatalf("install = %#v, %v", r, err)
	}
	b, err := os.ReadFile(grokHooksPath())
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("hooks file is not valid JSON: %v\n%s", err, b)
	}
	want := map[string]string{
		"SessionStart":     "hook-context",
		"UserPromptSubmit": "hook-prompt",
		"PreCompact":       "hook-precompact",
		"PreToolUse":       "hook-tool",
	}
	for event, sub := range want {
		groups, ok := root.Hooks[event]
		if !ok || len(groups) == 0 || len(groups[0].Hooks) == 0 {
			t.Fatalf("no %s hook:\n%s", event, b)
		}
		if got := groups[0].Hooks[0].Command; got == "" || groups[0].Hooks[0].Type != "command" {
			t.Fatalf("%s hook is not a command: %q\n%s", event, got, b)
		}
		if !containsSub(groups[0].Hooks[0].Command, sub) {
			t.Errorf("%s runs %q, want it to call %s", event, groups[0].Hooks[0].Command, sub)
		}
	}
	// The point-of-action hook must be scoped, or it fires on every read.
	if m := root.Hooks["PreToolUse"][0].Matcher; m == "" {
		t.Errorf("PreToolUse has no matcher, so it fires on reads too:\n%s", b)
	}

	if r, err = installGrokAuto("/bin/deja", false); err != nil || r.Action != "unchanged" {
		t.Fatalf("second install = %#v, %v", r, err)
	}
	if r, err = installGrokAuto("/bin/deja", true); err != nil || r.Action != "removed" {
		t.Fatalf("uninstall = %#v, %v", r, err)
	}
	if _, err := os.Stat(grokHooksPath()); !os.IsNotExist(err) {
		t.Fatalf("hooks file survived uninstall: %v", err)
	}
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
