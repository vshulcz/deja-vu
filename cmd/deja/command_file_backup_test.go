package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The four rules #2575 to #2596 established for configs and skills — recognise
// what is deja's, restore what install replaced, drop deja's own backup, remove
// what deja created — were applied one file at a time. The command files were
// never given them: install overwrites a `deja.md` the reader wrote, keeps the
// backup it promised, and uninstall deletes its own copy without putting theirs
// back. Eight files are lost that way in a full install/uninstall round (#2600).
func TestUninstallPutsBackACommandFileItReplaced(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	path := filepath.Join(home, ".cursor", "commands", "deja.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "# my own deja command\n\nThis file is mine.\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "cursor", "--no-index"); err != nil {
		t.Fatal(err)
	}
	// The premise: deja replaced it and kept a copy.
	if b, err := os.ReadFile(path); err != nil || string(b) == mine {
		t.Fatalf("install did not replace the reader's command file: %v", err)
	}
	if b, err := os.ReadFile(path + ".bak"); err != nil || string(b) != mine {
		t.Fatalf("install kept no copy of it: %v", err)
	}

	if _, err := captureRun(t, "uninstall", "cursor"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the reader's own command file is gone: %v", err)
	}
	if string(b) != mine {
		t.Errorf("what is there now is not theirs:\n%s", b)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("the copy was put back and the backup left behind: %v", err)
	}
}

// A command file deja wrote itself still goes, with no leftovers.
func TestUninstallRemovesACommandFileItWrote(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if _, err := captureRun(t, "install", "cursor", "--no-index"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".cursor", "commands", "deja.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("install wrote no command file: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "cursor"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("deja's own command file survived: %v", err)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("a backup survived: %v", err)
	}
}
