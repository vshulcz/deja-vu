package sources

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gooseStore writes a store the way goose writes one: sessions.updated_at in
// sqlite's CURRENT_TIMESTAMP format ("2026-07-27 15:29:47"), messages carrying
// unix seconds for the same instant. Measured on a real store.
func gooseStore(t *testing.T, db string, turns []struct {
	Session, Role, Text string
	At                  int64
}) {
	t.Helper()
	stmts := `create table if not exists sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table if not exists messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
`
	for _, turn := range turns {
		stmts += fmt.Sprintf(
			"insert or ignore into sessions values ('%[1]s','n','a goose session','/tmp/app',datetime(%[3]d,'unixepoch'),datetime(%[3]d,'unixepoch'));\n"+
				"update sessions set updated_at = datetime(%[3]d,'unixepoch') where id='%[1]s';\n"+
				"insert into messages (session_id,role,content_json,created_timestamp) values ('%[1]s','%[2]s',json_array(json_object('type','text','text','%[4]s')),%[3]d);\n",
			turn.Session, turn.Role, turn.At, turn.Text)
	}
	if out, err := exec.Command("sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
}

// A turn goose stored without a timestamp of its own is reachable only through
// its session, so the session-level half of the since clause is what finds it —
// and it has to compare two timestamps written in the same format. A real store
// keeps updated_at as sqlite's own "2026-07-27 15:29:47", which never compares
// greater than an RFC3339 string within the same day, so the turn was invisible
// to every incremental pass until the store was touched on a later date
// (#2030).
func TestGooseSinceStillFindsATurnWithNoTimestamp(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	db := filepath.Join(t.TempDir(), "sessions.db")
	type turn = struct {
		Session, Role, Text string
		At                  int64
	}
	first := int64(1785166187)
	gooseStore(t, db, []turn{{"g1", "user", "why does pgbouncer time out", first}})

	// A turn goose stored without a usable timestamp, on a session whose
	// updated_at moved a few minutes later — the same day.
	later := first + 600
	stmts := fmt.Sprintf(
		"insert into messages (session_id,role,content_json,created_timestamp) values ('g1','assistant',json_array(json_object('type','text','text','the pool is too small')),0);\n"+
			"update sessions set updated_at = datetime(%d,'unixepoch') where id='g1';\n", later)
	if out, err := exec.Command("sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}

	got, err := ParseGooseDBSince(db, time.Unix(first, 0))
	if err != nil {
		t.Fatal(err)
	}
	// The turn itself, not just something: a clause that handed back the
	// session's other message and stopped would answer this with the wrong one.
	found := false
	for _, s := range got {
		for _, msg := range s.Messages {
			if strings.Contains(msg.Text, "the pool is too small") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("a turn with no timestamp of its own is reachable only through its session, and it did not come back")
	}
}
