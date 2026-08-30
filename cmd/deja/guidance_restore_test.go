package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Install replaces a guidance file it did not write and says where the reader's
// copy went: "your copy is at …SKILL.md.bak". Uninstall then removed deja's
// file and left that copy sitting beside nothing — so a person who had written
// their own deja-history skill lost it by installing and uninstalling, with
// only a .bak and no line saying to rename it (#2581).
func TestUninstallPutsBackTheGuidanceItReplaced(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	skill := filepath.Join(home, ".claude", "skills", "deja-history")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(skill, "SKILL.md")
	mine := "---\nname: deja-history\ndescription: my own version\n---\n\nThis file is mine.\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := captureRun(t, "install", "claude-code", "--no-index"); err != nil {
		t.Fatal(err)
	}
	// The premise: deja replaced it and kept the copy it promised.
	if b, err := os.ReadFile(path); err != nil || string(b) == mine {
		t.Fatalf("install did not replace the reader's skill: %v", err)
	}
	if b, err := os.ReadFile(path + ".bak"); err != nil || string(b) != mine {
		t.Fatalf("install did not keep the copy it promised: %v", err)
	}

	if _, err := captureRun(t, "uninstall", "claude-code"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the reader's own skill is gone after uninstall: %v", err)
	}
	if string(b) != mine {
		t.Errorf("the file at that path is not the reader's own:\n%s", b)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("the copy was put back and the backup left behind too: %v", err)
	}
}

// A guidance file deja wrote itself leaves nothing behind: no file, no backup.
func TestUninstallRemovesGuidanceItWroteWithNoLeftovers(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if _, err := captureRun(t, "install", "claude-code", "--no-index"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "skills", "deja-history", "SKILL.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("install wrote no guidance: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "claude-code"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("deja's own guidance survived the uninstall: %v", err)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("a backup of deja's own guidance survived: %v", err)
	}
}
