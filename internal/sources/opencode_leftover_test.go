package sources

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// opencodeLeftoverFixture is a store the 2.0 migration left holding the old
// `session` table beside `session_v2`, with message and part gone. The 2.0.12
// store described in the v2 header has no `session` at all, and a store
// upgraded from 1.x keeps message and part — but a store whose migration
// dropped the turn tables and left `session` behind does occur in the wild,
// and indexing stopped for one the day its store reached that shape: the
// probe saw `session` still there, read sessions from it, and every session
// made after the migration was invisible. The leftover is frozen at migration
// time; the copy in session_v2 is the one opencode keeps writing to.
func opencodeLeftoverFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text primary key, project_id text, parent_id text, directory text, title text, time_created integer, time_updated integer);
create table session_v2(id text primary key, project_id text, parent_id text, directory text, title text, time_created integer, time_updated integer);
create table session_message(id text primary key, session_id text, type text, seq integer, time_created integer, time_updated integer, data text);
-- the leftover 1.x row: frozen at migration time, no v2-only sibling in the old table
insert into session values('old','p1',null,'/w/api','pgbouncer drops prepared statements',1767409200000,1767409300000);
-- the migration copied it under the same id and moved its turns
insert into session_v2 values('old','p1',null,'/w/api','pgbouncer drops prepared statements',1767409200000,1767409300000);
insert into session_message values('sm1','old','user',1,1767409201000,1767409201000,'{"time":{"created":1767409201000},"text":"why does pgbouncer drop prepared statements"}');
insert into session_message values('sm2','old','assistant',2,1767409202000,1767409202000,'{"time":{"created":1767409202000},"content":[{"type":"text","text":"transaction pooling, pin pgx to simple protocol"}]}');
-- a session made after the migration: absent from the old table entirely
insert into session_v2 values('new','p1',null,'/w/web','Dark mode toggle',1767409700000,1767409800000);
insert into session_message values('sm3','new','user',1,1767409701000,1767409701000,'{"time":{"created":1767409701000},"text":"add a dark mode toggle"}');
insert into session_message values('sm4','new','assistant',2,1767409702000,1767409702000,'{"time":{"created":1767409702000},"content":[{"type":"text","text":"added it to the settings page"}]}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	return db
}

func TestOpencodeReadsAStoreThatKeptTheOldSessionTable(t *testing.T) {
	db := opencodeLeftoverFixture(t)
	sc := opencodeSchemaOf(db)
	if !sc.v2 || sc.legacy || sc.mixed || sc.sessionTable != "session_v2" {
		t.Fatalf("schema = %+v, want 2.x only with sessions read from session_v2", sc)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ids := map[string]string{}
	for _, s := range ss {
		ids[s.ID] = s.Title
	}
	if got, ok := ids["old"]; !ok || got != "pgbouncer drops prepared statements" {
		t.Errorf("old = %q — the migrated session is still read, from session_v2", ids)
	}
	if got, ok := ids["new"]; !ok {
		t.Fatalf("sessions = %v, want the session made after the migration read from session_v2", ids)
	} else if got != "Dark mode toggle" {
		t.Errorf("new title = %q", got)
	}
}

func TestOpencodeSinceReadsANewSessionBehindALeftoverSessionTable(t *testing.T) {
	db := opencodeLeftoverFixture(t)
	// Between the migrated session and the one made after it: where the
	// watermark of an indexer that stopped at the migration date would sit.
	at := time.UnixMilli(1767409400000)
	ss, err := ParseOpencodeDBSince(db, at)
	if err != nil {
		t.Fatalf("parse since: %v", err)
	}
	var ids []string
	for _, s := range ss {
		ids = append(ids, s.ID)
	}
	if len(ids) != 1 || ids[0] != "new" {
		t.Fatalf("since = %v, want only new — a watermark at the migration date must still see it", ids)
	}
}
