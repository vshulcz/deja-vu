package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The launcher's own directory is deja's, and a full uninstall has to take it
// back. Found by installing everything into a stand and listing what was left:
// `~/.config/deja/bin` stood empty afterwards, because the directory was never
// recorded — #3698 prunes what the record names, and this one was not in it.
func TestUninstallTakesBackTheLauncherDirectory(t *testing.T) {
	path := dejaLauncherPath()
	if path == "" {
		t.Skip("no launcher on this platform")
	}
	tmp := hermeticEnv(t)
	_ = tmp
	path = dejaLauncherPath()
	dir := filepath.Dir(path)

	if _, err := writeDejaLauncher("/usr/local/bin/deja"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the launcher was not written: %v", err)
	}
	removeDejaLauncher()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the launcher is still there: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("%s stands empty after the launcher was removed", dir)
	}
}
