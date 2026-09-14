package sources

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// SQLite types values, not columns, so a driver that binds a message body as
// bytes writes a BLOB into a column declared TEXT. json_object refuses one
// outright — "JSON cannot hold BLOB values" — and that fails the whole query,
// which takes a harness out of recall while doctor still calls its store
// healthy. The body columns are cast, and this is the store that proves it.
func TestAStoreThatKeepsItsBodiesAsBlobsStillReads(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()

	t.Run("hermes", func(t *testing.T) {
		db := filepath.Join(tmp, "hermes.db")
		writeStore(t, db, `create table messages(id integer primary key, session_id text, role text, content text, timestamp real);
insert into messages(session_id,role,content,timestamp) values('h1','user',cast('blob store needle' as blob),1767409200);`)
		ss, err := ParseHermesDB(db)
		if err != nil || len(ss) != 1 {
			t.Fatalf("len=%d err=%v", len(ss), err)
		}
		if !strings.Contains(ss[0].Messages[0].Text, "needle") {
			t.Fatalf("session = %#v", ss[0])
		}
	})

	t.Run("goose", func(t *testing.T) {
		db := filepath.Join(tmp, "goose.db")
		writeStore(t, db, `create table sessions(id text, working_dir text, description text, created_at text, updated_at text);
create table messages(id integer primary key, session_id text, role text, content_json text, created_timestamp integer);
insert into sessions values('g1','/w','a goose session','2026-01-02T03:00:00Z','2026-01-02T04:00:00Z');
insert into messages(session_id,role,content_json,created_timestamp) values('g1','user',
  cast('[{"type":"text","text":"blob store needle"}]' as blob),1767409200);`)
		ss, err := parseGooseDBWhere(db, "", 0)
		if err != nil || len(ss) != 1 {
			t.Fatalf("len=%d err=%v", len(ss), err)
		}
		if !strings.Contains(ss[0].Messages[0].Text, "needle") {
			t.Fatalf("session = %#v", ss[0])
		}
	})
}

func writeStore(t *testing.T, db, script string) {
	t.Helper()
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
}
