package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Install writes skills into directories that belong to the user, and replaced
// one whenever its contents differed from what deja generates. That is right
// for a stale copy an older deja wrote and wrong for a file someone edited, and
// nothing on disk told them apart: a person who tuned the wording of their
// skill lost it on the next install, silently, with no way to notice except
// reading the file again.
func TestAnEditedSkillIsKept(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(t.TempDir(), "skills", "deja-history", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	ours := []byte("---\nname: deja-history\n---\n\nask deja first.\n")
	if _, err := writeGuidanceFile(path, nil, ours); err != nil {
		t.Fatal(err)
	}

	edited := []byte("---\nname: deja-history\n---\n\nask deja first, and always in Russian.\n")
	if err := os.WriteFile(path, edited, 0o600); err != nil {
		t.Fatal(err)
	}

	next := []byte("---\nname: deja-history\n---\n\nask deja first, then blame.\n")
	action, err := writeGuidanceFile(path, edited, next)
	if err != nil {
		t.Fatal(err)
	}
	if action != "kept" {
		t.Errorf("an edited skill reported %q, not kept", action)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "always in Russian") {
		t.Errorf("the user's own wording was overwritten:\n%s", got)
	}
}

// A copy deja wrote is still refreshed, or an improvement to the skill never
// reaches anyone who left it alone.
func TestAStaleSkillOfOursIsRefreshed(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(t.TempDir(), "skills", "deja-history", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	ours := []byte("old wording\n")
	if _, err := writeGuidanceFile(path, nil, ours); err != nil {
		t.Fatal(err)
	}
	next := []byte("new wording\n")
	if _, err := writeGuidanceFile(path, ours, next); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new wording\n" {
		t.Errorf("a skill deja wrote was not refreshed: %q", got)
	}
}

// And --force is how someone asks for deja's version back.
func TestForceTakesTheSkillBack(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(t.TempDir(), "skills", "deja-history", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := writeGuidanceFile(path, nil, []byte("ours\n")); err != nil {
		t.Fatal(err)
	}
	edited := []byte("mine\n")
	if err := os.WriteFile(path, edited, 0o600); err != nil {
		t.Fatal(err)
	}
	forceGuidance = true
	defer func() { forceGuidance = false }()
	if _, err := writeGuidanceFile(path, edited, []byte("ours again\n")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "ours again\n" {
		t.Errorf("--force did not take the skill back: %q", got)
	}
}

// A block inside the user's own AGENTS.md is edited surgically and was never at
// risk, so it must not start being refused.
func TestABlockInSomeoneElsesFileIsStillWritten(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(path, []byte("their notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeGuidanceFile(path, []byte("their notes\n"), []byte("their notes\nand deja's block\n")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "deja's block") {
		t.Errorf("a guidance block was refused as if it were a skill: %q", got)
	}
}
