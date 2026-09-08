package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The maintained Grok CLI writes one SQLite store beside the config and no
// session files at all, so on that build the row read `sessions=0 messages=0
// size=0 B` while doctor named the store and search answered from it. The row
// has to load what the registry loads (#3225).
func TestSourcesCountsGroksDatabase(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	home := hermeticEnv(t)
	root := filepath.Join(home, "grok")
	t.Setenv("DEJA_GROK_ROOT", root)
	t.Setenv("DEJA_GROK_DB", "")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	schema := `
CREATE TABLE workspaces (id TEXT PRIMARY KEY, canonical_path TEXT);
CREATE TABLE sessions (id TEXT PRIMARY KEY, workspace_id TEXT, title TEXT, cwd_last TEXT, created_at TEXT);
CREATE TABLE messages (session_id TEXT, seq INTEGER, role TEXT, message_json TEXT, created_at TEXT);
INSERT INTO sessions VALUES ('s1','w1','Pool timeouts','/work/api','2026-07-27T10:00:00.000Z');
INSERT INTO messages VALUES ('s1',0,'user','{"role":"user","content":"zibblex pool exhausted again"}','2026-07-27T10:00:00.000Z');
INSERT INTO sessions VALUES ('s2','w1','Retry cap','/work/api','2026-07-28T10:00:00.000Z');
INSERT INTO messages VALUES ('s2',0,'user','{"role":"user","content":"the retry cap is four"}','2026-07-28T10:00:00.000Z');
`
	cmd := exec.Command("sqlite3", filepath.Join(root, "grok.db"))
	cmd.Stdin = strings.NewReader(schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}

	// The row itself, not the loader beside it: the count comes out of
	// printSources, and a test on the pieces would have passed while the row
	// printed zeros.
	out := captureStdout(t, func() { printSources(filepath.Join(home, "index.db")) })
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "grok\t") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no grok row:\n%s", out)
	}
	if strings.Contains(line, "sessions=0") || strings.Contains(line, "size=0 B") {
		t.Errorf("the row does not count the database:\n%s", line)
	}

	var loaded int
	for _, s := range append(sources.LoadGrok(), sources.LoadGrokDB()...) {
		loaded += len(s.Messages)
	}
	if loaded == 0 {
		t.Fatal("the fixture produced nothing, so the row proves nothing")
	}
	files := grokReadFiles()
	found := false
	for _, p := range files {
		if filepath.Base(p) == "grok.db" {
			found = true
		}
	}
	if !found {
		t.Errorf("the row's file list has no grok.db, so the size column reads 0 B: %v", files)
	}
	if n := filesSize(presentFiles(files...)); n == 0 {
		t.Errorf("the row measures %d bytes with a seeded database on disk", n)
	}
}
