package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Qwen consumes what a PostToolUse hook returns, and deja was not listening: a
// qwen session ran a failing command and heard nothing, while the store held
// the command that settled the same error. Measured on qwen-code 0.20.0 against
// a stub endpoint — the hook fires and its context reaches the model, unlike
// PreToolUse, which fires and whose output does not.
func TestInstallQwenWiresTheFixPair(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_QWEN_ROOT", "")
	if _, err := installQwenAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".qwen", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	entries := root.Hooks["PostToolUse"]
	if len(entries) != 1 {
		t.Fatalf("PostToolUse entries = %d, want 1: %s", len(entries), b)
	}
	if got := entries[0].Hooks[0].Command; !strings.HasSuffix(got, "hook-tool-after") {
		t.Errorf("PostToolUse runs %q, want the fix pair", got)
	}
	if m := entries[0].Matcher; m != "run_shell_command" {
		t.Errorf("matcher = %q, want the tool that runs commands", m)
	}
	// PreToolUse fires on qwen too, and what it returns never reaches the
	// model — a process per command for nothing.
	if len(root.Hooks["PreToolUse"]) != 0 {
		t.Errorf("PreToolUse is wired, and its output is not context: %s", b)
	}
	// Reinstalling must not double any of them.
	if _, err := installQwenAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b2, err := os.ReadFile(filepath.Join(home, ".qwen", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != string(b2) {
		t.Errorf("a second install changed the file:\n%s\n%s", b, b2)
	}
}

// Qwen frames a command's output as a labelled report before handing it to the
// model. The "Output:" label in front of the first line is what stopped a build
// failure reading as an error, so the fix pair said nothing about a failure the
// store had already settled.
func TestQwenShellReportIsUnwrapped(t *testing.T) {
	raw := json.RawMessage(`{"llmContent":"Command: go build ./...\nDirectory: (root)\nOutput: ./main.go:12:2: undefined: zorbquuxHelper\nError: (none)\nExit Code: 0\nSignal: 0\nProcess Group PGID: 2726"}`)
	got := strings.TrimSpace(toolResponseText(raw))
	if got != "./main.go:12:2: undefined: zorbquuxHelper" {
		t.Errorf("qwen's shell report was not unwrapped: %q", got)
	}
}
