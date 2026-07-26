package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallClaudeCommandsWritesDiscoverableCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installClaudeCommands("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	// Claude Code only picks up ~/.claude/commands/*.md.
	path := filepath.Join(home, ".claude", "commands", "deja.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("command not at the discovered path: %v", err)
	}
	src := string(b)
	// Frontmatter is what names the command in the "/" menu.
	if !strings.HasPrefix(src, "---\nname: deja\n") {
		t.Fatalf("missing frontmatter:\n%s", src)
	}
	if !strings.Contains(src, "$ARGUMENTS") {
		t.Fatalf("command ignores the user's arguments:\n%s", src)
	}
	if !strings.Contains(src, "/bin/deja") {
		t.Fatalf("CLI fallback does not name the binary:\n%s", src)
	}
	if _, err := installClaudeCommands("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("command survived uninstall: %v", err)
	}
	if r, err := installClaudeCommands("/bin/deja", true); err != nil || r.Action != "unchanged" {
		t.Fatalf("second uninstall = %+v, %v", r, err)
	}
}

func TestClaudeAutoInstallsCommandAlongsideHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installClaudeAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, p := range []string{
		filepath.Join(home, ".claude", "commands", "deja.md"),
		filepath.Join(home, ".claude", "settings.json"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("claude-auto did not write %s: %v", p, err)
		}
	}
}
