package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Aider reads this file in every session because the config points at it, and
// only `deja aider` ever rewrites it. Install runs before there is an index, so
// the placeholder is what a user who starts plain `aider` reads forever —
// driven through the real interface it said "No matching history yet.", which
// reads as deja having nothing rather than as nothing having refreshed it.
func TestAiderPlaceholderSaysHowToFillIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(home, "no-history"))

	if err := refreshAiderContext(filepath.Join(home, "index.db")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(aiderContextPath())
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	if !strings.Contains(body, "deja aider") {
		t.Fatalf("the placeholder does not say what would fill it:\n%s", body)
	}
	if strings.TrimSpace(body) == "" {
		t.Fatal("the file must exist and be non-empty; aider refuses to start without it")
	}
}
