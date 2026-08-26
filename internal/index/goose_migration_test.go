package index

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// goose 1.10 moved its sessions into sessions.db and left the JSONL files
// behind, so the same conversation is in both stores under one id. That is a
// migration, not two transcripts clashing: the database is the live copy, and
// deja used to report every migrated session as an id collision and file the
// row under the superseded file, because the file name sorts first (#2066).
func TestAMigratedGooseSessionIsNotACollision(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none-opencode.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	root := filepath.Join(tmp, "goose")
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

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// The premise: both copies really did reach the same row, so the pair is
	// the one the fix is about.
	s, ok, err := FindByIdentity(dir, "goose", "20260101_120000")
	if err != nil || !ok {
		t.Fatalf("the session is not indexed at all, so this measures nothing: ok=%v err=%v", ok, err)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("the two stores did not merge into one session: %d messages", len(s.Messages))
	}

	if n := ReportCollisions(); n != 0 {
		t.Errorf("a migrated session was counted as %d id collision(s)", n)
	}
	if !strings.HasSuffix(s.Path, "sessions.db") {
		t.Errorf("the row is filed under %s, not the live store", s.Path)
	}
	if shared := HarnessSharedCounts(dir)["goose"]; shared != 0 {
		t.Errorf("doctor reports %d shared goose session(s)", shared)
	}
}

// A genuine clash between two transcripts must still be reported: two JSONL
// files under one id are two conversations, whatever the migration looks like.
func TestTwoGooseFilesUnderOneIDStillCollide(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none-opencode.db"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	root := filepath.Join(tmp, "goose")
	sessions := filepath.Join(root, "sessions", "archive")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_GOOSE_ROOT", root)
	for i, dirName := range []string{"a", "b"} {
		d := filepath.Join(sessions, dirName)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"id":"20260101_120000","description":"pgbouncer pool","working_dir":"/tmp/app` + dirName + `"}
{"role":"user","content":[{"type":"text","text":"conversation ` + dirName + `"}],"created":176726880` + string(rune('0'+i)) + `}
`
		if err := os.WriteFile(filepath.Join(d, "20260101_120000.jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if n := ReportCollisions(); n == 0 {
		t.Error("two transcripts sharing an id were not reported at all")
	}
}
