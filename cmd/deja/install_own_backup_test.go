package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A config deja wrote whole and then edited again leaves a snapshot on the
// second write. Removing the wiring empties the file, which is the path that
// deletes it — and it returned before the snapshot rule ran, so uninstall left
// a .bak holding nothing but deja's own block and called it a config the
// reader already had (#3340).
func TestUninstallTakesItsOwnSnapshotWithIt(t *testing.T) {
	hermeticEnv(t)
	if _, err := installDeepSeekMCP("/bin/deja", false); err != nil {
		t.Fatalf("install deepseek: %v", err)
	}
	// The second write is what snapshots the file deja itself created.
	if _, err := installDeepSeekAuto("/bin/deja", false); err != nil {
		t.Fatalf("install deepseek-auto: %v", err)
	}
	patch := filepath.Join(sources.DSHHome(), "cordis.patch.yml")
	if _, err := os.Stat(patch + ".bak"); err != nil {
		t.Skipf("this install left no snapshot to take back: %v", err)
	}

	removingWiring = true
	defer func() { removingWiring = false }()
	if _, err := installDeepSeekAuto("/bin/deja", true); err != nil {
		t.Fatalf("uninstall deepseek-auto: %v", err)
	}
	if _, err := installDeepSeekMCP("/bin/deja", true); err != nil {
		t.Fatalf("uninstall deepseek: %v", err)
	}
	if b, err := os.ReadFile(patch + ".bak"); err == nil {
		t.Errorf("uninstall kept a snapshot of deja's own block:\n%s", b)
	}
}

// The other half of the same rule: a config the reader already had keeps its
// snapshot, whatever deja did to the live file.
func TestUninstallKeepsASnapshotOfTheirOwnConfig(t *testing.T) {
	hermeticEnv(t)
	dir := sources.DSHHome()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	patch := filepath.Join(dir, "cordis.patch.yml")
	theirs := "- insert:\n    - id: my-plugin\n      name: \"my-plugin.js\"\n"
	if err := os.WriteFile(patch, []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installDeepSeekMCP("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	removingWiring = true
	defer func() { removingWiring = false }()
	if _, err := installDeepSeekMCP("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	b, err := os.ReadFile(patch + ".bak")
	if err != nil {
		t.Fatalf("uninstall took the snapshot of a config they already had: %v", err)
	}
	if string(b) != theirs {
		t.Errorf("their snapshot changed:\n%s", b)
	}
}
