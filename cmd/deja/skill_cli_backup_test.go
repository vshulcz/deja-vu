package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The CLI skill gets the same treatment as the MCP one on the way in — replaced
// with a backup when deja has no record of writing it — and none of it on the
// way out. #2581 taught the deja-history path to put the reader's copy back and
// #2585 taught it to drop a copy that is deja's own; deja-search was left with
// neither, so an older deja's own skill survives an uninstall (#2596).
func TestUninstallHandlesTheCLISkillsBackupToo(t *testing.T) {
	t.Run("an older deja's own copy goes", func(t *testing.T) {
		home := cliSkillFixture(t, cliSkillFileWithBody("An older deja wrote this body."))
		path := filepath.Join(home, ".agents", "skills", "deja-search", "SKILL.md")
		if _, err := os.Stat(path + ".bak"); err != nil {
			t.Fatalf("install kept no backup, so this measures nothing: %v", err)
		}
		if _, err := captureRun(t, "uninstall", "codex"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("deja's own CLI skill survived: %v", err)
		}
		if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
			t.Errorf("a backup of deja's own CLI skill survived the uninstall")
		}
	})

	t.Run("the reader's own copy comes back", func(t *testing.T) {
		mine := "---\nname: deja-search\ndescription: my own version\n---\n\nThis file is mine.\n"
		home := cliSkillFixture(t, mine)
		path := filepath.Join(home, ".agents", "skills", "deja-search", "SKILL.md")
		if _, err := captureRun(t, "uninstall", "codex"); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil || string(b) != mine {
			t.Errorf("the reader's own CLI skill did not come back: %v\n%s", err, b)
		}
	})
}

// cliSkillFixture puts a deja-search skill in place, installs over it, and
// returns the home it did that in.
func cliSkillFixture(t *testing.T, content string) string {
	t.Helper()
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".agents", "skills", "deja-search")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "codex", "--no-index"); err != nil {
		t.Fatal(err)
	}
	return home
}

// cliSkillFileWithBody is deja's own CLI skill with a different body, which is
// what an older build left behind.
func cliSkillFileWithBody(body string) string {
	return "---\nname: " + cliSkillName + "\ndescription: " + cliSkillDesc + "\n---\n\n" + body + "\n"
}
