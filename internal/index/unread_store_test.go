package index

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A store deja could not read must not be filed as read. opencode and Cursor
// hold their sqlite stores while they run, and a lock outlasting the timeout
// used to be recorded with the store's size and mtime — so the next pass
// found nothing changed, called the index up to date, and the history stayed
// missing until someone rebuilt by hand (#3176).
func TestAStoreThatCouldNotBeReadIsNotFiledAsRead(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "opencode.db")
	if err := os.WriteFile(db, []byte("garbage, not a database, sixty-four bytes of nothing useful....."), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCODE_DB", db)

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	for p := range m.Files {
		if strings.Contains(p, "opencode.db") {
			t.Fatalf("a store that could not be read was filed as read: %s", p)
		}
	}
}
