package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A `kiro-cli chat --no-interactive` run lives in data.sqlite3, keyed by the
// directory it ran in. Resume took the directory from the JSONL header beside
// a transcript, which a database row has none of, so the command ran wherever
// the reader stood and kiro-cli rewrote the session's cwd to it (#4305, #4300).
func TestResumeKiroDBSessionRunsInItsDirectory(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "proj's app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(tmp, "data.sqlite3")
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	seed := "CREATE TABLE conversations_v2 (key TEXT NOT NULL, conversation_id TEXT NOT NULL, value TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, PRIMARY KEY (key, conversation_id));\n" +
		"INSERT INTO conversations_v2 VALUES (" + q(proj) + ",'bf73e1f5-0000-4000-8000-000000000002','{}',1,1);"
	cmd := exec.Command("sqlite3", db)
	cmd.Stdin = strings.NewReader(seed)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	t.Setenv("DEJA_KIRO_DB", db)

	dir, line, err := resumeCommand(model.Session{Harness: "kiro", ID: "bf73e1f5-0000-4000-8000-000000000002", Path: db})
	if err != nil {
		t.Fatal(err)
	}
	if dir != proj || line != "kiro-cli chat --resume-id bf73e1f5-0000-4000-8000-000000000002" {
		t.Errorf("got (%q, %q), want the row's directory %q", dir, line, proj)
	}

	// An id that is not in the table, or one carrying a quote, finds nothing.
	for _, id := range []string{"0000", "x' OR '1'='1"} {
		if dir, _, _ := resumeCommand(model.Session{Harness: "kiro", ID: id, Path: db}); dir != "" {
			t.Errorf("id %q resumed in %q", id, dir)
		}
	}
}
