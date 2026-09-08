package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A host with no settings file gets one from deja, then a second write in the
// same run allowed deja's tool — and snapshotted deja's own file from a moment
// before as a .bak the uninstall then called a config the reader already had.
func TestInstallRooDoesNotSnapshotItsOwnFreshWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_ROO_ROOTS", strings.Join([]string{rooStorage(t, home, "Code")}, string(os.PathListSeparator)))
	if _, err := installRoo("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	baks, _ := filepath.Glob(filepath.Join(home, "Library", "Application Support", "Code", "User", "globalStorage", "rooveterinaryinc.roo-cline", "settings", "*.bak"))
	if len(baks) != 0 {
		t.Fatalf("a snapshot of deja's own fresh write: %v", baks)
	}
}

// A settings file the reader already had still gets its snapshot on the first
// write, as before.
func TestInstallRooStillSnapshotsAFileTheReaderHad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	store := rooStorage(t, home, "Code")
	t.Setenv("DEJA_ROO_ROOTS", store)
	path := filepath.Join(store, "settings", "mcp_settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	theirs := `{"mcpServers":{"other":{"command":"x","args":[]}}}` + "\n"
	if err := os.WriteFile(path, []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installRoo("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path + ".bak"); err != nil || string(got) != theirs {
		t.Fatalf("the reader's file was not snapshotted before the first write: %v %q", err, got)
	}
}
