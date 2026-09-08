package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The grok row loaded the JSONL reader alone; a Grok Build store in grok.db
// read as sessions=0 while doctor and search saw it (#3225).
func TestSourcesCountsGrokDBSessions(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HERMES_HOME", "")
	db := filepath.Join(tmp, "grok.db")
	t.Setenv("DEJA_GROK_DB", db)
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(tmp, "grok"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	sql := `CREATE TABLE workspaces (id TEXT PRIMARY KEY, canonical_path TEXT);
CREATE TABLE sessions (id TEXT PRIMARY KEY, workspace_id TEXT, title TEXT, cwd_last TEXT, created_at TEXT);
CREATE TABLE messages (session_id TEXT, seq INTEGER, role TEXT, message_json TEXT, created_at TEXT);
INSERT INTO sessions VALUES ('g1','w','Pool timeouts','/work/api','2026-07-27T10:00:00.000Z');
INSERT INTO messages VALUES ('g1',0,'user','{"content":"why does the pool time out"}','2026-07-27T10:00:00.000Z');
INSERT INTO sessions VALUES ('g2','w','Quiet one','/work/api','2026-07-26T10:00:00.000Z');
INSERT INTO messages VALUES ('g2',0,'user','{"content":"a second session"}','2026-07-26T10:00:00.000Z');`
	cmd := exec.Command("sqlite3", db)
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v: %s", err, out)
	}
	out := captureStdout(t, func() { printSources(filepath.Join(tmp, "index.db")) })
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "grok\t") {
			if !strings.Contains(l, "sessions=2") || strings.Contains(l, "size=0 B") {
				t.Fatalf("grok row misses the store's sessions: %s", l)
			}
			return
		}
	}
	t.Fatalf("no grok row in:\n%s", out)
}
