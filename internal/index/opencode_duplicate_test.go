package index

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// seedTimelessOpencodeSession writes a session with nothing for the index to
// stamp as a watermark: no time on the part, none on the message. A store like
// this is re-read whole on every pass, which is the case that showed the
// records piling up (#2033).
func seedTimelessOpencodeSession(t *testing.T, db, id, text string) {
	t.Helper()
	stmts := fmt.Sprintf(`
create table if not exists session (id text primary key, directory text, time_created integer, time_updated integer);
create table if not exists message (id text primary key, session_id text, data text, time_created integer);
create table if not exists part (id text primary key, message_id text, data text);
insert into session values ('%[1]s','/tmp/app',0,0);
insert into message values ('m-%[1]s','%[1]s','{"role":"user"}',0);
insert into part values ('p-%[1]s','m-%[1]s',json_object('type','text','text','%[2]s'));
`, id, text)
	if out, err := exec.Command("sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
}

// A session that comes back from the store again must replace what the index
// holds, not join it. opencode's records escaped that rule because it is keyed
// on the record's source path and opencode puts the project directory there,
// so a store re-read whole grew by a copy of every session on every pass.
func TestAnOpencodeSessionReadAgainDoesNotDouble(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "opencode.db")
	t.Setenv("DEJA_OPENCODE_DB", db)

	seedTimelessOpencodeSession(t, db, "s1", "why does pgbouncer time out")
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	messages := func() int {
		t.Helper()
		s, ok, err := FindByIdentity(dir, "opencode", "s1")
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatal("the session is not in the index")
		}
		return len(s.Messages)
	}
	if got := messages(); got != 1 {
		t.Fatalf("the build indexed %d messages for a one-turn session, so this measures nothing", got)
	}
	// The premise: nothing to stamp, so the next pass reads the whole store
	// rather than the part that changed.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if stamp := m.Files[db].LastUpdated; stamp != 0 {
		t.Fatalf("the store stamped a watermark of %d, so the pass below will not re-read it whole", stamp)
	}

	// Two more passes, each triggered by a session that has nothing to do with
	// the first one.
	for i, id := range []string{"s2", "s3"} {
		seedTimelessOpencodeSession(t, db, id, "an unrelated session")
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		if got := messages(); got != 1 {
			t.Fatalf("one turn on disk, %d in the index after pass %d", got, i+1)
		}
	}
}

// The other side of the same rule: a store read from a watermark hands back the
// new turns alone, so dropping its old records by key would take the rest of the
// session with them. This is the common case — every continued opencode session.
func TestAContinuedOpencodeSessionKeepsItsEarlierTurns(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "opencode.db")
	t.Setenv("DEJA_OPENCODE_DB", db)

	seedOpencodeSession(t, db, "s1", "the first turn is about pgbouncer", 1785166187000)
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The premise, and the difference from the case above: this store has a
	// watermark, so the next pass reads only what is newer than it.
	if m.Files[db].LastUpdated == 0 {
		t.Fatal("the store has no watermark, so the pass below reads it whole and measures the other case")
	}

	stmts := `insert into message values ('m2-s1','s1','{"role":"assistant","time":{"created":1785166787000}}',1785166787000);
insert into part values ('p2-s1','m2-s1',json_object('type','text','text','the second turn is the answer','time',json_object('start',1785166787000)));
update session set time_updated=1785166787000 where id='s1';`
	if out, err := exec.Command("sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v %s", err, out)
	}
	var said strings.Builder
	if err := Ensure(dir, "", false, &said); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(said.String(), "incremental index") {
		t.Fatalf("this was not the merge path, so it does not measure what it is about: %q", said.String())
	}
	s, ok, err := FindByIdentity(dir, "opencode", "s1")
	if err != nil || !ok {
		t.Fatalf("the session did not come back: %v %v", ok, err)
	}
	if len(s.Messages) != 2 {
		texts := []string{}
		for _, msg := range s.Messages {
			texts = append(texts, msg.Text)
		}
		t.Errorf("the session continued and came back with %d turns: %v", len(s.Messages), texts)
	}
	if hits, err := Search(dir, search.Options{Query: "pgbouncer", All: true}); err != nil || len(hits) == 0 {
		t.Errorf("the first turn is no longer searchable (%d hits, %v)", len(hits), err)
	}
}
