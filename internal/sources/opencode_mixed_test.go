package sources

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// opencodeMixedFixture is a store upgraded from 1.x to 2.0 (#4151): the 2.0
// migration creates session_v2 beside session and keeps message and part, and
// the v1 migration copies a session into session_v2 under the same id with its
// turns rewritten into session_message.
//
//	old   1.x only: session, message, part
//	moved in both tables; the copy in session_message is the one opencode keeps
//	new   2.x only: session_v2, session_message
func opencodeMixedFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text primary key, project_id text, parent_id text, directory text, title text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, time_updated integer, data text);
create table part(id text primary key, message_id text, session_id text, time_created integer, time_updated integer, data text);
create table session_v2(id text primary key, project_id text, parent_id text, directory text, title text, time_created integer, time_updated integer);
create table session_message(id text primary key, session_id text, type text, seq integer, time_created integer, time_updated integer, data text);
insert into session values('old','p1',null,'/w/api','pgbouncer drops prepared statements',1767409200000,1767409300000);
insert into message values('m1','old',1767409201000,1767409201000,'{"role":"user","time":{"created":1767409201000}}');
insert into part values('p1','m1','old',1767409201000,1767409201000,'{"type":"text","text":"why does pgbouncer drop prepared statements","time":{"start":1767409201000}}');
insert into message values('m2','old',1767409202000,1767409202000,'{"role":"assistant","time":{"created":1767409202000}}');
insert into part values('p2','m2','old',1767409202000,1767409202000,'{"type":"text","text":"transaction pooling, pin pgx to simple protocol","time":{"start":1767409202000}}');
insert into part values('p3','m2','old',1767409203000,1767409203000,'{"type":"tool","tool":"bash","state":{"input":{"command":"go test ./db/..."},"output":"ok","metadata":{"exit":0}},"time":{"start":1767409203000}}');
insert into session values('moved','p1','old','/w/api','Flaky retry test',1767409400000,1767409500000);
insert into message values('m3','moved',1767409401000,1767409401000,'{"role":"user","time":{"created":1767409401000}}');
insert into part values('p4','m3','moved',1767409401000,1767409401000,'{"type":"text","text":"why does TestRetry flake","time":{"start":1767409401000}}');
insert into session_v2 values('moved','p1','old','/w/api','Flaky retry test in payments',1767409400000,1767409600000);
insert into session_message values('sm1','moved','user',1,1767409401000,1767409401000,'{"time":{"created":1767409401000},"text":"why does TestRetry flake"}');
insert into session_message values('sm2','moved','assistant',2,1767409550000,1767409550000,'{"time":{"created":1767409550000},"content":[{"type":"text","text":"the backoff timer ignores the context deadline"}]}');
insert into session_v2 values('new','p1',null,'/w/web','Dark mode toggle',1767409700000,1767409800000);
insert into session_message values('sm3','new','user',1,1767409701000,1767409701000,'{"time":{"created":1767409701000},"text":"add a dark mode toggle"}');
insert into session_message values('sm4','new','assistant',2,1767409702000,1767409702000,'{"time":{"created":1767409702000},"content":[{"type":"text","text":"added it to the settings page"}]}');
insert into session_message values('sm5','new','model-switched',3,1767409703000,1767409703000,'{}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	return db
}

func TestOpencodeReadsAStoreHoldingBothLayouts(t *testing.T) {
	db := opencodeMixedFixture(t)
	sc := opencodeSchemaOf(db)
	if !sc.v2 || !sc.legacy || sc.sessionTable != "session_v2" {
		t.Fatalf("schema = %+v, want both layouts with sessions in session_v2", sc)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var ids []string
	texts := map[string][]string{}
	for _, s := range ss {
		ids = append(ids, s.ID)
		for _, m := range s.Messages {
			texts[s.ID] = append(texts[s.ID], m.Role+": "+m.Text)
		}
	}
	sort.Strings(ids)
	if strings.Join(ids, ",") != "moved,new,old" {
		t.Fatalf("sessions = %v, want each of old, moved and new once", ids)
	}
	if got := strings.Join(texts["old"], " | "); !strings.Contains(got, "why does pgbouncer drop") ||
		!strings.Contains(got, "pin pgx to simple protocol") || !strings.Contains(got, "$ go test ./db/...") {
		t.Errorf("old = %q — a 1.x session is read from message and part", got)
	}
	// Read from session_message only: the 1.x copy would repeat the question
	// and miss the answer written after the upgrade.
	if got := texts["moved"]; len(got) != 2 || got[0] != "user: why does TestRetry flake" ||
		got[1] != "assistant: the backoff timer ignores the context deadline" {
		t.Errorf("moved = %q", got)
	}
	if got := texts["new"]; len(got) != 2 {
		t.Errorf("new = %q", got)
	}
	for _, s := range ss {
		switch s.ID {
		case "moved":
			if s.Title != "Flaky retry test in payments" {
				t.Errorf("moved title = %q — the 2.0 copy is the one opencode renames", s.Title)
			}
			if s.Parent != "old" || s.Kind != "subagent" {
				t.Errorf("moved parent = %q kind = %q", s.Parent, s.Kind)
			}
		case "old":
			if s.Title != "pgbouncer drops prepared statements" {
				t.Errorf("old title = %q — titles come from session too", s.Title)
			}
		}
	}

	t.Setenv("DEJA_OPENCODE_DB", db)
	sessions, messages, err := OpencodeCounts()
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	// 3 sessions, not 4: moved is in both tables. Messages are old's two text
	// parts plus the four user and assistant turns in session_message.
	if sessions != 3 || messages != 6 {
		t.Errorf("counts = %d sessions %d messages, want 3 and 6", sessions, messages)
	}

	newest, err := ParseOpencodeNewest(db)
	if err != nil || len(newest) != 1 || newest[0].ID != "new" {
		t.Fatalf("newest = %v err=%v", newest, err)
	}

	// The watermark sits after old's turns and before the answer in moved:
	// only what changed since comes back, from either layout.
	since, err := ParseOpencodeDBSince(db, time.UnixMilli(1767409500000))
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	ids = ids[:0]
	for _, s := range since {
		ids = append(ids, s.ID)
	}
	sort.Strings(ids)
	if strings.Join(ids, ",") != "moved,new" {
		t.Errorf("since = %v, want moved and new", ids)
	}
	before, err := ParseOpencodeDBSince(db, time.UnixMilli(1767409000000))
	if err != nil || len(before) != 3 {
		t.Errorf("since before everything: len=%d err=%v", len(before), err)
	}
}

// A session opened and left empty is often the newest row, and reading it
// alone is what made doctor call a working store parsed-zero (#4151). The
// newest session with turns answers instead, on every layout.
func TestOpencodeNewestSkipsASessionWithNoTurns(t *testing.T) {
	empty := func(t *testing.T, db, table string) {
		t.Helper()
		// Newer than anything the fixtures hold, and with no turns.
		sql := `insert into ` + table + `(id, directory, title, time_created, time_updated) values('empty','/w','New session',1799999999000,1799999999000);`
		if out, err := exec.Command("sqlite3", db, sql).CombinedOutput(); err != nil {
			t.Fatalf("sqlite: %v %s", err, out)
		}
	}
	cases := []struct {
		name  string
		db    func(t *testing.T) string
		table string
		want  string
	}{
		{"mixed", opencodeMixedFixture, "session_v2", "new"},
		{"mixed, empty 1.x row", opencodeMixedFixture, "session", "new"},
		{"2.x", opencodeV2Fixture, "session_v2", "s1"},
		{"1.x", opencodeV1Fixture, "session", "s1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := c.db(t)
			empty(t, db, c.table)
			ss, err := ParseOpencodeNewest(db)
			if err != nil || len(ss) != 1 || ss[0].ID != c.want || len(ss[0].Messages) == 0 {
				t.Fatalf("newest = %+v err=%v, want %s with its turns", ss, err, c.want)
			}
		})
	}
}

func opencodeV1Fixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text primary key, directory text, title text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, data text);
create table part(id text primary key, message_id text, data text);
insert into session values('s1','/w','the payments suite',1767409200000,1767409300000);
insert into message values('m1','s1',1767409201000,'{"role":"user","time":{"created":1767409201000}}');
insert into part values('p1','m1','{"type":"text","text":"why does TestRetry flake","time":{"start":1767409201000}}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	return db
}

// A session prefix and a search reach both layouts.
func TestOpencodeMixedStoreAnswersWhereClauses(t *testing.T) {
	db := opencodeMixedFixture(t)
	ss, err := ParseOpencodeDBWhere(db, " and s.id like 'ol%'", 0)
	if err != nil || len(ss) != 1 || ss[0].ID != "old" {
		t.Fatalf("prefix: %v err=%v", ss, err)
	}
	ss, err = ParseOpencodeDBWhere(db, " and lower(p.data) like '%backoff%'", 0)
	if err != nil || len(ss) != 1 || ss[0].ID != "moved" {
		t.Fatalf("search: %v err=%v", ss, err)
	}
}
