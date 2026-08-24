package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deja cannot tell its own pre-marks copy from a file someone else wrote at the
// same path, and treating every unmarked file as a stranger's would freeze the
// skill on every machine installed before marks existed. What it can do is say
// what it replaced: this came out as "guidance updated", with the backup
// unmentioned (#1703).
func TestInstallReportsReplacingAnUnmarkedSkill(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".claude", "skills", "deja-history")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	const mine = "---\nname: deja-history\ndescription: MY OWN skill\n---\n\nI wrote this by hand.\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "install", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "replaced") {
		t.Errorf("the run does not say a file was replaced:\n%s", out)
	}
	if !strings.Contains(out, ".bak") {
		t.Errorf("the run does not name the backup:\n%s", out)
	}
	b, readErr := os.ReadFile(path + ".bak")
	if readErr != nil {
		t.Fatalf("no backup was written: %v", readErr)
	}
	if string(b) != mine {
		t.Errorf("the backup is not what the user had:\n%s", b)
	}
}

// backupOnce keeps the first .bak it ever wrote, so a second replacement — a
// machine whose mark record was lost — would have promised a copy of the first
// file while the one being destroyed went unsaved (#1703, found in review).
func TestReplacedSkillBackupHoldsWhatWasReplaced(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".claude", "skills", "deja-history")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte("A: my first file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-code"); err != nil {
		t.Fatal(err)
	}
	// The marks record is lost — a wiped cache — and a different file is there.
	if err := os.Remove(skillMarksPath()); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	const second = "C: my second file, written later\n"
	if err := os.WriteFile(path, []byte(second), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-code"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != second {
		t.Errorf("the backup holds %q, not the file that was just replaced", string(b))
	}
}

// The control: a file deja wrote and recorded is updated quietly, and a fresh
// install with no file says nothing about replacing anything.
func TestInstallDoesNotCryReplacedForItsOwnSkill(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "replaced") {
		t.Errorf("a first install claims to have replaced something:\n%s", out)
	}
	out, err = captureRun(t, "install", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "replaced") {
		t.Errorf("a second install claims to have replaced something:\n%s", out)
	}
}
