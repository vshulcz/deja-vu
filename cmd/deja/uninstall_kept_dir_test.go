package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The prune on the way out says "when deja made it and nothing else is in it"
// and could only test the second half: os.Remove refuses a directory with
// anything in it, and nothing recorded who made it. A host's own empty folder
// — a VS Code `User` directory a reader created and had not filled yet — went
// with the uninstall (#3239).
func TestUninstallKeepsADirectoryTheReaderAlreadyHad(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home")
	dir := filepath.Join(home, ".config", "deja-fixture")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")

	if _, err := writeIfChanged(path, nil, []byte(`{"mcpServers":{"deja":{}}}`)); err != nil {
		t.Fatal(err)
	}
	recordWiring([]string{"claude-code"}, false)

	removingWiring = true
	defer func() { removingWiring = false }()
	if _, err := writeIfChanged(path, []byte(`{"mcpServers":{"deja":{}}}`), []byte(`{"mcpServers":{}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("the config deja created outlived the uninstall")
	}
	if !isRealDir(dir) {
		t.Error("uninstall removed a directory the reader made")
	}
}

// And one deja had to make goes, which is the case the prune was written for.
func TestUninstallRemovesTheDirectoryItMade(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home")
	dir := filepath.Join(home, ".config", "deja-fixture", "nested")
	path := filepath.Join(dir, "settings.json")

	if _, err := writeIfChanged(path, nil, []byte(`{"mcpServers":{"deja":{}}}`)); err != nil {
		t.Fatal(err)
	}
	if !isRealDir(dir) {
		t.Fatal("the install did not create the directory")
	}
	recordWiring([]string{"claude-code"}, false)

	removingWiring = true
	defer func() { removingWiring = false }()
	if _, err := writeIfChanged(path, []byte(`{"mcpServers":{"deja":{}}}`), []byte(`{"mcpServers":{}}`)); err != nil {
		t.Fatal(err)
	}
	if isRealDir(dir) {
		t.Error("the directory deja made was left behind")
	}
}
