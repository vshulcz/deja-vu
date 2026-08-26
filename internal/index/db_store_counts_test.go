package index

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	t.Setenv("USERPROFILE", tmp)
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
