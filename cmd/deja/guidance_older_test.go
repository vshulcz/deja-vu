package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A skill an older deja wrote is deja's, not the reader's. Install treats it as
// a stranger's file — it carries no marks — backs it up, and since #2581
// uninstall puts that backup back: so uninstalling deja left deja's own older
// guidance on disk, which is what #2575 exists to prevent. Its own frontmatter
// says whose it is (#2585).
func TestUninstallDoesNotPutBackAnOlderDejaSkill(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	skill := filepath.Join(home, ".claude", "skills", "deja-history")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(skill, "SKILL.md")
	// deja's own frontmatter, an older body.
	older := skillFile("Before re-deriving past work, search deja when the user refers to past work.")
	if older == guidanceText("claude-code") {
		t.Fatal("the fixture is this build's own guidance, so this measures nothing")
	}
	if err := os.WriteFile(path, []byte(older), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-code", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("install did not back the older skill up: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "claude-code"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path); err == nil {
		t.Errorf("an older deja skill was put back after uninstall:\n%s", strings.SplitN(string(b), "\n\n", 2)[0])
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("its backup is still there: %v", err)
	}
}

// And a skill the reader wrote is still theirs, even where they kept deja's
// name for the directory.
func TestUninstallStillPutsBackTheReadersOwnSkill(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	skill := filepath.Join(home, ".claude", "skills", "deja-history")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(skill, "SKILL.md")
	mine := "---\nname: deja-history\ndescription: my own version\n---\n\nAsk deja first. This file is mine.\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-code", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "claude-code"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != mine {
		t.Errorf("the reader's own skill did not come back: %v\n%s", err, b)
	}
}
