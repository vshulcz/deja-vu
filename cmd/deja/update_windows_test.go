//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveStaleUpdateBackups(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "deja.exe")
	backups := []string{
		filepath.Join(dir, ".deja.exe.old-.deja-update-one"),
		filepath.Join(dir, ".deja.exe.old-.deja-update-two"),
	}
	for _, backup := range backups {
		if err := os.WriteFile(backup, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	unrelated := filepath.Join(dir, "keep.exe")
	if err := os.WriteFile(unrelated, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	manualBackup := filepath.Join(dir, ".deja.exe.old-manual")
	if err := os.WriteFile(manualBackup, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	removeStaleUpdateBackups(destination)
	for _, backup := range backups {
		if _, err := os.Stat(backup); !os.IsNotExist(err) {
			t.Fatalf("backup still exists: %s", backup)
		}
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal("cleanup removed unrelated file", err)
	}
	if _, err := os.Stat(manualBackup); err != nil {
		t.Fatal("cleanup removed manual backup", err)
	}
}
