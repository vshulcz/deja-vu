package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Copilot Chat has no hook, so the only thing that can sit in front of the model
// before it reads the question is a custom-instructions file. VS Code applies
// one to every chat when its frontmatter says `applyTo: "**"` — measured on
// 1.134.0, with this file alone in the User folder's prompts directory the agent
// called deja on a question that had nothing to do with past work.
func TestVSCodeGuidanceIsAnAlwaysAppliedInstructionsFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_VSCODE_USER_DIRS", dir)

	path := guidancePath("vscode")
	if want := filepath.Join(dir, "prompts", "deja.instructions.md"); path != want {
		t.Fatalf("guidance goes to %q, want %q — VS Code reads the prompts folder", path, want)
	}
	text := guidanceText("vscode")
	if !strings.HasPrefix(text, "---\napplyTo: \"**\"\n---") {
		t.Errorf("no applyTo frontmatter, so the file applies to nothing:\n%s", text)
	}
	if strings.Contains(text, "name: deja-history") {
		t.Errorf("wrote skill frontmatter into an instructions file:\n%s", text)
	}
	// The block is only worth having if it names the thing to call.
	if !strings.Contains(text, "deja") {
		t.Errorf("the instructions never mention deja:\n%s", text)
	}
}

// install writes it, and uninstall takes it away: a stale always-applied file
// would keep telling the agent to call a server that is no longer wired.
func TestVSCodeGuidanceIsWrittenAndRemoved(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, "User")
	t.Setenv("DEJA_VSCODE_USER_DIRS", dir)

	if _, err := guidanceResult("vscode", false); err != nil {
		t.Fatalf("guidance install: %v", err)
	}
	path := filepath.Join(dir, "prompts", "deja.instructions.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("guidance not written where VS Code reads it: %v", err)
	}
	if !strings.Contains(string(b), "applyTo") {
		t.Errorf("the written file has no applyTo:\n%s", b)
	}

	if _, err := guidanceResult("vscode", true); err != nil {
		t.Fatalf("guidance uninstall: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the always-applied file survived uninstall: %v", err)
	}
}
