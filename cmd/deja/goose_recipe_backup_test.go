package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The goose recipe is the last file with the broken promise #2600 named:
// install replaces the one already at that path and keeps the copy it promises,
// uninstall removes deja's version and leaves the reader with a .bak beside
// nothing (#2602).
func TestUninstallPutsBackTheGooseRecipeItReplaced(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	path := filepath.Join(home, ".config", "goose", "deja-recipe.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "# my own recipe\nversion: 1.0.0\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "goose", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path + ".bak"); err != nil || string(b) != mine {
		t.Fatalf("install kept no copy of the recipe it replaced: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "goose"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the reader's own recipe is gone: %v", err)
	}
	if string(b) != mine {
		t.Errorf("what is at that path is not theirs:\n%s", b)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("the copy was put back and the backup left behind: %v", err)
	}
}

// A recipe deja wrote itself goes, with nothing left beside it.
func TestUninstallRemovesTheGooseRecipeItWrote(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if _, err := captureRun(t, "install", "goose", "--no-index"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".config", "goose", "deja-recipe.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("install wrote no recipe: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "goose"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("deja's own recipe survived: %v", err)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("a backup survived: %v", err)
	}
}
