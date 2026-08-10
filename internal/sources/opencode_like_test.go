package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A search term is matched literally: the LIKE wildcards _ and % in it must not
// widen the match. Before the escape an id prefix `a_b` also resolved `axb`, and
// a query holding a `%` matched every row.
func TestLoadOpencodeMatchingEscapesLikeWildcards(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('a_b','/tmp/p','2026-01-02T03:00:00Z','2026-01-02T03:00:00Z');
insert into session values('axb','/tmp/p','2026-01-02T03:00:00Z','2026-01-02T03:00:00Z');
insert into message values('m1','a_b',1,'{"role":"user"}');
insert into message values('m2','axb',1,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"literal a_b marker"}');
insert into part values('p2','m2','{"type":"text","text":"literal axb marker"}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v %s", err, out)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_OPENCODE_DB", db)

	got := LoadOpencodeMatching("a_b")
	if len(got) != 1 || got[0].ID != "a_b" {
		t.Fatalf("query a_b matched %d sessions, the _ wildcard leaked axb: %#v", len(got), got)
	}

	// The id prefix takes the same escaping, so a_b must not resolve axb.
	pre := LoadOpencodePrefix("a_b")
	if len(pre) != 1 || pre[0].ID != "a_b" {
		t.Fatalf("prefix a_b matched %d sessions: %#v", len(pre), pre)
	}
}
