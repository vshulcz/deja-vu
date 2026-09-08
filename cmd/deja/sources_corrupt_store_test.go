package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The reason on the row is what sqlite3 said, not its exit code (#3190).
func TestSourcesNamesSqlitesReasonForAStoreThatIsNotADatabase(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "opencode.db")
	if err := os.WriteFile(db, []byte("garbage, not a database, sixty-four bytes of nothing useful....."), 0o644); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_OPENCODE_DB", db)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	out := captureStdout(t, func() { printSources(filepath.Join(tmp, "index.db")) })
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "opencode\t") {
			if !strings.Contains(l, "cannot be read — sqlite3: ") || !strings.Contains(l, "not a database") {
				t.Fatalf("row does not carry sqlite's reason: %s", l)
			}
			return
		}
	}
	t.Fatalf("no opencode row in:\n%s", out)
}
