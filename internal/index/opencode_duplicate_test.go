package index

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
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
