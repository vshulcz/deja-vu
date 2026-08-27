package index

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// seedOpencodeSession adds one session with one text part to an opencode store,
// creating the tables if the file is new.
func seedOpencodeSession(t *testing.T, db, session, text string, createdMillis int64) {
	t.Helper()
	stmts := fmt.Sprintf(`
create table if not exists session (id text primary key, directory text, time_created integer, time_updated integer);
create table if not exists message (id text primary key, session_id text, data text, time_created integer);
create table if not exists part (id text primary key, message_id text, data text);
insert into session values ('%[1]s','/tmp/app',%[2]d,%[2]d);
insert into message values ('m-%[1]s','%[1]s','{"role":"user","time":{"created":%[2]d}}',%[2]d);
insert into part values ('p-%[1]s','m-%[1]s',json_object('type','text','text','%[3]s','time',json_object('start',%[2]d)));
`, session, createdMillis, text)
	if out, err := exec.Command("sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
}

// A database-backed store reads only what is new — the since cursor the index
// stamps as LastUpdated — but it changes like any other file, so the merge
// branch treated it as re-read whole and started its ingest counts over. The
// database growing by one session threw away what the rest of it holds (#2025).
func TestADatabaseThatGrowsKeepsItsIngestCounts(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "opencode.db")
	t.Setenv("DEJA_OPENCODE_DB", db)

	long := strings.Repeat("pgbouncer pool timed out and the retry took a second ", 1600)
	if len(long) < maxIndexedText {
		t.Fatalf("the fixture message is %d bytes, under the %d that gets it clipped", len(long), maxIndexedText)
	}
	seedOpencodeSession(t, db, "s1", long, 1767322800000)

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	clipped := func() int {
		t.Helper()
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		return m.IngestHealth["opencode"].ClippedMessages
	}
	if got := clipped(); got != 1 {
		t.Fatalf("the build clipped %d messages, so this measures nothing", got)
	}

	// The store grows by a session that has nothing wrong with it. The pass
	// reads only that session, so it cannot speak for the rest of the store.
	seedOpencodeSession(t, db, "s2", "a short second session", 1767326400000)
	var out strings.Builder
	if err := Ensure(dir, "", false, &out); err != nil {
		t.Fatal(err)
	}
	if said := out.String(); !strings.Contains(said, "incremental index") {
		t.Fatalf("this was not the merge path, so it does not measure what it is about: %q", said)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Sessions) != 2 {
		t.Fatalf("the pass ended with %d sessions, so the store did not grow the way this expects", len(m.Sessions))
	}
	if got := clipped(); got != 1 {
		t.Errorf("the clipped message is still in the database and the count is %d", got)
	}
}

// seedGooseTurn adds one message to a goose store, creating the tables if the
// file is new and moving the session's updated_at with it.
func seedGooseTurn(t *testing.T, db, session, role, text string, created int64) {
	t.Helper()
	stmts := fmt.Sprintf(`
create table if not exists sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table if not exists messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert or ignore into sessions values ('%[1]s','g','a goose session','/tmp/app', datetime(%[4]d,'unixepoch'), datetime(%[4]d,'unixepoch'));
update sessions set updated_at = datetime(%[4]d,'unixepoch') where id='%[1]s';
insert into messages (session_id,role,content_json,created_timestamp) values ('%[1]s','%[2]s',json_array(json_object('type','text','text','%[3]s')),%[4]d);
`, session, role, text, created)
	if out, err := exec.Command("sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
}

// goose asks for everything in a session that was touched, not only the new
// messages, so a continued session hands back turns already counted. With
// nothing resetting a database's counts (#2025) those turns were counted again
// on every pass — a store in daily use would report clips in the thousands.
//
// The timestamps are sqlite's own format, which is what a real store holds; the
// since clause puts both sides through datetime(), so the format no longer
// decides the comparison (#2030).
func TestAGooseSessionThatContinuesIsNotCountedTwice(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "goose.db")
	t.Setenv("DEJA_GOOSE_DB", db)

	long := strings.Repeat("pgbouncer pool timed out and the retry took a second ", 1600)
	seedGooseTurn(t, db, "g1", "user", long, 1767322800)
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	clipped := func() int {
		t.Helper()
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		// By the store, which is the key doctor files these under since #2234
		// — "goose-db" is the file kind.
		return m.IngestHealth["goose"].ClippedMessages
	}
	if got := clipped(); got != 1 {
		t.Fatalf("the build clipped %d messages, so this measures nothing", got)
	}

	// The session carries on. Each pass hands the long message back again.
	for i, ts := range []int64{1767326400, 1767330000} {
		seedGooseTurn(t, db, "g1", "assistant", "a short answer", ts)
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		if got := clipped(); got != 1 {
			t.Fatalf("one long message in the store, pass %d says %d clipped", i+1, got)
		}
	}
}

// The other side of handing back only the new turns: what the index already
// holds has to survive it. A partial return that replaced the session would
// drop every turn it did not include (#2032).
func TestAGooseSessionKeepsItsEarlierTurns(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "goose.db")
	t.Setenv("DEJA_GOOSE_DB", db)

	seedGooseTurn(t, db, "g1", "user", "why does pgbouncer time out", 1785166187)
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if hits, err := Search(dir, search.Options{Query: "pgbouncer", All: true}); err != nil || len(hits) == 0 {
		t.Fatalf("the first turn is not in the index (%d hits, %v), so this measures nothing", len(hits), err)
	}

	seedGooseTurn(t, db, "g1", "assistant", "the pool was too small", 1785166787)
	var out strings.Builder
	if err := Ensure(dir, "", false, &out); err != nil {
		t.Fatal(err)
	}
	if said := out.String(); !strings.Contains(said, "incremental index") {
		t.Fatalf("this was not the merge path, so it does not measure what it is about: %q", said)
	}
	s, ok, err := FindByIdentity(dir, "goose", "g1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || len(s.Messages) != 2 {
		t.Fatalf("the session came back with %d messages (found=%v): the new turn replaced what was there", len(s.Messages), ok)
	}
	if hits, err := Search(dir, search.Options{Query: "pgbouncer", All: true}); err != nil || len(hits) == 0 {
		t.Errorf("the first turn is gone from the index after the session continued (%d hits, %v)", len(hits), err)
	}
}
