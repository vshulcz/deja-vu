package sources

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ZCode's CLI keeps a SQLite store beside the transcripts and it went unread
// while no sample of the schema was in hand. The schema is OpenCode's — the
// same reader Kilo's CLI uses — attested by zcode-stats 0.8.0, which reads the
// live database and queries `session(directory, task_type, …)`,
// `message(id, session_id, data)` and `part(data)` (#3675).
//
// Skipped without sqlite3, like every other database store here.
func TestZCodeReadsTheCLIDatabase(t *testing.T) {
	if !SQLite3Available() {
		t.Skip("sqlite3 CLI not installed")
	}
	home := t.TempDir()
	db := filepath.Join(home, "db.sqlite")
	t.Setenv("DEJA_ZCODE_DB", db)
	t.Setenv("DEJA_ZCODE_ROOT", filepath.Join(home, "absent"))

	schema := `create table session (id text primary key, directory text, time_created any, time_updated any);
create table message (id text primary key, session_id text, time_created any, data text);
create table part (id text primary key, message_id text, data text);
insert into session values ('zc-1', '/work/api', '2026-09-17T09:00:00Z', '2026-09-17T09:00:02Z');
insert into message values ('m-user', 'zc-1', 1789650001000, '{"role":"user","time":{"created":"2026-09-17T09:00:01Z"}}');
insert into message values ('m-assistant', 'zc-1', 1789650002000, '{"role":"assistant","time":{"created":"2026-09-17T09:00:02Z"}}');
insert into part values ('p-user', 'm-user', '{"type":"text","text":"why is the glm stream cut short","time":{"start":"2026-09-17T09:00:01Z"}}');
insert into part values ('p-assistant', 'm-assistant', '{"type":"text","text":"the proxy closed on an idle timeout","time":{"start":"2026-09-17T09:00:02Z"}}');
`
	if out, err := exec.Command("sqlite3", db, schema).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	ss := LoadZCode()
	if len(ss) == 0 {
		t.Fatal("the CLI database was not read")
	}
	for _, s := range ss {
		if s.Harness != "zcode" {
			t.Fatalf("harness = %q, want zcode", s.Harness)
		}
	}
	found := false
	for _, s := range ss {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, "idle timeout") {
				found = true
			}
		}
	}
	if !found {
		t.Error("the reply from the database is missing")
	}
	// And the file list names it, or an incremental ingest would never see it.
	named := false
	for _, p := range ZCodeSessionFiles() {
		if p == db {
			named = true
		}
	}
	if !named {
		t.Errorf("ZCodeSessionFiles does not name the database: %v", ZCodeSessionFiles())
	}
}
