package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A goose session that came back from both of that harness's stores is one
// conversation, so the id-collision warning is wrong about it — but the
// per-harness line still counted it twice, and the totals that reconcile the
// two used to print only alongside that warning (#2066, #1091).
func TestAMigratedGooseSessionSaysTheTotalsWithoutTheWarning(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	hermeticEnv(t)
	root := filepath.Join(t.TempDir(), "goose")
	sessions := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_GOOSE_ROOT", root)
	db := filepath.Join(sessions, "sessions.db")
	t.Setenv("DEJA_GOOSE_DB", db)

	jsonl := `{"id":"20260101_120000","description":"pgbouncer pool","working_dir":"/tmp/app"}
{"role":"user","content":[{"type":"text","text":"why does pgbouncer time out"}],"created":1767268800}
`
	if err := os.WriteFile(filepath.Join(sessions, "20260101_120000.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}
	sql := `create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert into sessions values ('20260101_120000','n','pgbouncer pool','/tmp/app','2026-01-01 12:00:00','2026-01-01 12:05:00');
insert into messages values (1,'20260101_120000','user','[{"type":"text","text":"why does pgbouncer time out"}]',1767268800);
insert into messages values (2,'20260101_120000','user','[{"type":"text","text":"and after the restart"}]',1767269100);`
	if out, err := exec.Command("sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}

	var err error
	stderr := captureStderr(t, func() { _, err = captureRun(t, "index") })
	if err != nil {
		t.Fatal(err)
	}
	// The premise: the per-harness line really did count both copies, so the
	// reconciling line has something to reconcile.
	if !strings.Contains(stderr, "goose: 2 sessions") {
		t.Fatalf("the two stores were not both read, so this measures nothing: %q", stderr)
	}
	if strings.Contains(stderr, "an id with another transcript") {
		t.Errorf("a migrated session was reported as an id collision: %q", strings.TrimSpace(stderr))
	}
	if !strings.Contains(stderr, "count transcripts, not rows") {
		t.Errorf("the per-harness line says 2 sessions and nothing says the index holds 1: %q", strings.TrimSpace(stderr))
	}
}
