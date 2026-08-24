package main

import (
	"os"
	"path/filepath"
	"testing"
)

// `deja uninstall --all` left ~/.agents/skills/deja-history/SKILL.md on disk
// with deja's guidance in it. The guard that protects the shared file from one
// harness leaving asked readWiringState(), which still lists every harness
// during the run that removes them all (#1683).
func TestUninstallAllRemovesTheSharedSkill(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	// codex is a shared-skill harness: its guidance lives in ~/.agents.
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "--all"); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(home, ".agents", "skills", "deja-history", "SKILL.md")
	if _, err := os.Stat(shared); err != nil {
		t.Fatalf("install did not write the shared skill, wrong fixture: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "--all"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(shared); err == nil {
		b, _ := os.ReadFile(shared)
		t.Errorf("uninstall --all left the shared skill behind:\n%s", string(b[:min(len(b), 200)]))
	}
}

// One harness leaving must still not take the file from the rest — that is what
// the guard is for, and the fix must not cost it.
func TestUninstallOneKeepsTheSharedSkillForTheOthers(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	for _, d := range []string{".codex", ".cursor"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "install", "--all"); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(home, ".agents", "skills", "deja-history", "SKILL.md")
	if _, err := captureRun(t, "uninstall", "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(shared); err != nil {
		t.Errorf("uninstalling codex took the shared skill from cursor: %v", err)
	}
}
