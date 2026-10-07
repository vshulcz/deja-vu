package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// goose hands its hooks no tool output, so the failure a Stop hook answers is
// read from the store: the newest failed shell response of the turn, never one
// from before the person's last message.
func TestGooseTurnFailureReadsTheTurnsFailedCommand(t *testing.T) {
	if !SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("DEJA_GOOSE_ROOT", filepath.Join(root, "goose"))
	dir := filepath.Join(root, "goose", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "sessions.db")
	fail := func(text string) string {
		return `[{"type":"toolResponse","id":"c","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"` + text + `"}],"structuredContent":{"exit_code":2},"isError":true}}}]`
	}
	sql := `create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert into sessions values ('s1','n','d','/work/app','2026-10-07T10:00:00Z','2026-10-07T10:05:00Z');
insert into sessions values ('s2','n','d','/work/app','2026-10-07T10:00:00Z','2026-10-07T10:05:00Z');
insert into messages values (1,'s1','user','[{"type":"text","text":"build it"}]',100);
insert into messages values (2,'s1','user','` + fail("old failure from turn one") + `',101);
insert into messages values (3,'s1','user','[{"type":"text","text":"<turn-context>now</turn-context>and again"}]',102);
insert into messages values (4,'s1','assistant','[{"type":"toolRequest","id":"c","toolCall":{"status":"success","value":{"name":"developer__shell","arguments":{"command":"make"}}}}]',103);
insert into messages values (5,'s1','user','` + fail("panic: sql: database is closed") + `',104);
insert into messages values (6,'s2','user','[{"type":"text","text":"build it"}]',100);
insert into messages values (7,'s2','user','` + fail("old failure from turn one") + `',101);
insert into messages values (8,'s2','user','[{"type":"text","text":"something else"}]',102);`
	if out, err := exec.Command("sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("create db: %v: %s", err, out)
	}
	if got := GooseTurnFailure("s1"); got != "panic: sql: database is closed" {
		t.Errorf("s1 turn failure = %q", got)
	}
	if got := GooseTurnFailure("s2"); got != "" {
		t.Errorf("a failure from before the person's last message came back: %q", got)
	}
	if got := GooseTurnFailure("missing"); got != "" {
		t.Errorf("an unknown session answered %q", got)
	}
}
