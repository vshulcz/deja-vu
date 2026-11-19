package sources

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func opencodeDBHome(t *testing.T) (home, xdg string) {
	t.Helper()
	home, xdg = t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_OPENCODE_DB", "")
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("XDG_DATA_HOME", xdg)
	return home, xdg
}

func touchDB(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// opencode takes its data directory from xdg-basedir, which reads
// XDG_DATA_HOME on every OS. deja read it on Linux only, so a mac with the
// variable set indexed nothing from opencode and its compaction packet came
// back empty.
func TestOpencodeDBFollowsXDGDataHomeOnEveryOS(t *testing.T) {
	_, xdg := opencodeDBHome(t)
	want := filepath.Join(xdg, "opencode", "opencode.db")
	touchDB(t, want)
	if got := OpencodeDB(); got != want {
		t.Fatalf("OpencodeDB() = %q, want %q", got, want)
	}
	if got, w := OpencodeDiffDir(), filepath.Join(xdg, "opencode", "storage", "session_diff"); got != w {
		t.Fatalf("OpencodeDiffDir() = %q, want %q", got, w)
	}
}

// opencode started from a GUI without the shell's XDG_DATA_HOME keeps its
// store in the default directory; that store is still read.
func TestOpencodeDBFallsBackWhenTheXDGStoreIsMissing(t *testing.T) {
	home, xdg := opencodeDBHome(t)
	def := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	touchDB(t, def)
	if got := OpencodeDB(); got != def {
		t.Fatalf("OpencodeDB() = %q, want the default store %q", got, def)
	}
	// With neither present the XDG path is the answer, as opencode would
	// create it there.
	if err := os.Remove(def); err != nil {
		t.Fatal(err)
	}
	if got, want := OpencodeDB(), filepath.Join(xdg, "opencode", "opencode.db"); got != want {
		t.Fatalf("OpencodeDB() = %q, want %q", got, want)
	}
}

func TestOpencodeDBHonoursOpencodeDBVariable(t *testing.T) {
	_, xdg := opencodeDBHome(t)
	abs := filepath.Join(t.TempDir(), "elsewhere.db")
	t.Setenv("OPENCODE_DB", abs)
	if got := OpencodeDB(); got != abs {
		t.Fatalf("absolute OPENCODE_DB: got %q, want %q", got, abs)
	}
	t.Setenv("OPENCODE_DB", "custom.db")
	if got, want := OpencodeDB(), filepath.Join(xdg, "opencode", "custom.db"); got != want {
		t.Fatalf("relative OPENCODE_DB: got %q, want %q", got, want)
	}
	t.Setenv("DEJA_OPENCODE_DB", "/pinned.db")
	if got := OpencodeDB(); got != "/pinned.db" {
		t.Fatalf("DEJA_OPENCODE_DB must win, got %q", got)
	}
}

const opencodeDiffFixture = `[
  {"file": "internal/pool/pool.go", "status": "modified", "additions": 2, "deletions": 1,
   "patch": "--- a/internal/pool/pool.go\n+++ b/internal/pool/pool.go\n@@ -40,1 +40,2 @@\n-\tcfg.MaxConnLifetime = 30 * time.Minute\n+\tcfg.MaxConnLifetime = 4 * time.Minute\n+\tcfg.HealthCheckPeriod = 30 * time.Second\n"},
  {"file": "internal/queue/worker.go", "status": "added", "additions": 3, "deletions": 0,
   "patch": "--- /dev/null\n+++ b/internal/queue/worker.go\n@@ -0,0 +1,3 @@\n+package queue\n+\n+func Work() {}\n"}
]`

// The diff store is the only account of what most opencode sessions changed —
// 17 of 400 sampled sessions held an edit record from the database, and 384 of
// them are in the index (#3791). So what it parses has to arrive as records on
// the session the database already gave, keyed on the same id.
func TestOpencodeDiffStoreYieldsTheSessionsOwnChanges(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_OPENCODE_DIFFS", dir)
	path := filepath.Join(dir, "ses_1a3dd1e75ffeOKtQ9luutwjr7S.json")
	if err := os.WriteFile(path, []byte(opencodeDiffFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseOpencodeDiff(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("parsed %d sessions, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "opencode" {
		t.Errorf("harness %q", s.Harness)
	}
	if s.ID != "ses_1a3dd1e75ffeOKtQ9luutwjr7S" {
		t.Errorf("id %q — it has to be the id the database uses, or this arrives as a second session", s.ID)
	}

	var edits, files int
	for _, m := range s.Messages {
		switch m.Role {
		case RoleEdit:
			edits++
			// "path\nreplaced bytes", the shape every other harness's edits
			// take, so restore and blame need not know where it came from.
			path, span, ok := strings.Cut(m.Text, "\n")
			if !ok {
				t.Errorf("edit record has no path line: %q", m.Text)
			}
			if path != "internal/pool/pool.go" {
				t.Errorf("edit record names %q", path)
			}
			if !strings.Contains(span, "30 * time.Minute") {
				t.Errorf("the replaced span is %q, and the removed line is what a diff holds", span)
			}
			if strings.Contains(span, "+") || strings.Contains(span, "4 * time.Minute") {
				t.Errorf("the added side leaked into the replaced span: %q", span)
			}
		case RoleFiles:
			files++
			for _, want := range []string{"internal/pool/pool.go", "internal/queue/worker.go"} {
				if !strings.Contains(m.Text, want) {
					t.Errorf("the files record does not name %s: %q", want, m.Text)
				}
			}
		}
	}
	// One patch removes a line and one is a new file, so exactly one edit.
	if edits != 1 {
		t.Errorf("%d edit records, want 1: a new file removes nothing", edits)
	}
	if files != 1 {
		t.Errorf("%d files records, want 1", files)
	}
}

// Most of those files are an empty list — the session changed nothing — and an
// empty session would add a row to every screen that counts sessions.
func TestAnEmptyDiffFileIsNotASession(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_OPENCODE_DIFFS", dir)
	for name, body := range map[string]string{
		"ses_empty.json":   "[]",
		"ses_broken.json":  "{not json",
		"notasession.json": opencodeDiffFixture,
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		ss, err := ParseOpencodeDiff(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(ss) != 0 {
			t.Errorf("%s produced %d sessions, want none", name, len(ss))
		}
	}
}

// The store is found from the database path, so every override carries over:
// a second variable to keep in step is how two paths drift apart.
func TestTheDiffStoreFollowsTheDatabasePath(t *testing.T) {
	t.Setenv("DEJA_OPENCODE_DIFFS", "")
	t.Setenv("DEJA_OPENCODE_DB", filepath.FromSlash("/tmp/stand/opencode.db"))
	want := filepath.FromSlash("/tmp/stand/storage/session_diff")
	if got := OpencodeDiffDir(); got != want {
		t.Errorf("OpencodeDiffDir() = %q, want %q", got, want)
	}

	dir := t.TempDir()
	t.Setenv("DEJA_OPENCODE_DIFFS", dir)
	if err := os.WriteFile(filepath.Join(dir, "ses_a.json"), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := OpencodeDiffFiles()
	if len(got) != 1 || filepath.Base(got[0]) != "ses_a.json" {
		t.Errorf("OpencodeDiffFiles() = %v, want the one session file", got)
	}
}

// opencodeV2Many writes a 2.x store of n sessions with the index opencode
// creates on session_message, plus ids holding LIKE wildcards and a quote.
func opencodeV2Many(t *testing.T, n int) (string, []string) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	var b strings.Builder
	b.WriteString(`create table session_v2(id text primary key, project_id text, parent_id text, directory text, title text, time_created integer, time_updated integer);
create table session_message(id text primary key, session_id text not null, type text not null, seq integer, time_created integer, time_updated integer, data text not null);
create index session_message_session_type_seq_idx on session_message(session_id,type,seq);
begin;
`)
	ids := make([]string, 0, n+4)
	for i := range n {
		ids = append(ids, fmt.Sprintf("ses_%04d", i))
	}
	ids = append(ids, "ses_a_b", "ses_axb", "ses_a%c", "ses_o'q")
	for i, id := range ids {
		q := strings.ReplaceAll(id, "'", "''")
		at := int64(1767409200000) + int64(i)*60000
		fmt.Fprintf(&b, "insert into session_v2 values('%s','p1',null,'/w','session %d',%d,%d);\n", q, i, at, at+3000)
		fmt.Fprintf(&b, "insert into session_message values('%s-1','%s','user',1,%d,%d,'{\"time\":{\"created\":%d},\"text\":\"why does the pool %d leak\"}');\n", q, q, at, at, at, i)
		fmt.Fprintf(&b, "insert into session_message values('%s-2','%s','assistant',2,%d,%d,'{\"time\":{\"created\":%d},\"content\":[{\"type\":\"text\",\"text\":\"the pool %d is not closed\"},"+
			"{\"type\":\"tool\",\"id\":\"c1\",\"name\":\"bash\",\"state\":{\"status\":\"completed\",\"input\":{\"command\":\"go test ./pool/%d\"},\"content\":[{\"type\":\"text\",\"text\":\"--- FAIL: TestPool\"}],\"metadata\":{\"exit\":1}},\"time\":{\"start\":%d}}]}');\n",
			q, q, at+1000, at+1000, at+1000, i, i, at+2000)
		fmt.Fprintf(&b, "insert into session_message values('%s-3','%s','compaction',3,%d,%d,'{\"time\":{\"created\":%d},\"summary\":\"pool %d summary\"}');\n", q, q, at+3000, at+3000, at+3000, i)
	}
	b.WriteString("commit;\n")
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, b.String()).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	return db, ids
}

// A new session's lineage is read by its exact id. The read must return that
// session, and only it, the way the whole-store read sees it, wildcards and
// quotes in the id included (#4951).
func TestLoadOpencodeSessionReadsOneSessionAsTheStoreReadDoes(t *testing.T) {
	db, ids := opencodeV2Many(t, 20)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_OPENCODE_DB", db)
	all, err := ParseOpencodeDB(db)
	if err != nil || len(all) != len(ids) {
		t.Fatalf("whole store: len=%d err=%v", len(all), err)
	}
	for _, want := range all {
		got := LoadOpencodeSession(want.ID)
		if len(got) != 1 {
			t.Fatalf("%q: got %d sessions", want.ID, len(got))
		}
		if !reflect.DeepEqual(got[0], want) {
			t.Errorf("%q read alone differs from the store read:\n got %+v\nwant %+v", want.ID, got[0], want)
		}
	}
	if got := LoadOpencodeSession("ses_a"); len(got) != 0 {
		t.Errorf("an id is not a prefix: ses_a read %d sessions", len(got))
	}
}

// The restriction has to reach each session_message branch before its JSON is
// expanded. An s.id in (...) outside the CTE did not, and SQLite scanned and
// decoded every message of the store to return a few sessions (#4951).
func TestOpencodeV2SessionReadUsesTheMessageIndex(t *testing.T) {
	db, _ := opencodeV2Many(t, 3)
	only := "in ('ses_0001','ses_0002')"
	q := opencodeV2Query("session_v2", " and s.id "+only, 0, only)
	out, err := exec.CommandContext(t.Context(), "sqlite3", "-readonly", db, "explain query plan "+q).CombinedOutput()
	if err != nil {
		t.Fatalf("plan: %v %s", err, out)
	}
	plan := string(out)
	if strings.Contains(plan, "SCAN sm") {
		t.Errorf("a branch scans session_message:\n%s", plan)
	}
	if n := strings.Count(plan, "SEARCH sm USING INDEX session_message_session_type_seq_idx"); n != 3 {
		t.Errorf("%d of 3 branches search the index:\n%s", n, plan)
	}
}

// The since read names the changed sessions first and reads only them, each
// whole, the same as the whole-store read; with more of them than it names
// one by one it reads through the subquery and gets the same answer.
func TestOpencodeV2SinceReadsTheChangedSessionsWhole(t *testing.T) {
	db, ids := opencodeV2Many(t, 30)
	all, err := ParseOpencodeDB(db)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]int{}
	for i, s := range all {
		byID[s.ID] = i
	}
	// Sessions 25 and later started at least 25 minutes after the first.
	since := time.UnixMilli(1767409200000 + 25*60000).Add(opencodeSinceSlack)
	check := func(label string) {
		t.Helper()
		got, err := ParseOpencodeDBSince(db, since)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(ids)-25 {
			t.Fatalf("%s: read %d sessions, want %d", label, len(got), len(ids)-25)
		}
		for _, s := range got {
			i, ok := byID[s.ID]
			if !ok || !reflect.DeepEqual(s, all[i]) {
				t.Errorf("%s: %q differs from the whole-store read", label, s.ID)
			}
		}
	}
	check("named")
	defer func(n int) { opencodeSetCap = n }(opencodeSetCap)
	opencodeSetCap = 3
	check("past the cap")
}

// A part opencode marks `ignored` is one it does not feed to the model. A
// compression plugin's status line carries it and is filed under the user role,
// so those banners were indexed as the person's words — 376 of them on a real
// store, 20.8 KB, and the flag was exact there: every ignored part was one of
// these, and none of the 4,420 real user parts had it (#3515).
func TestOpencodeIgnoredPartsAreNotThePersons(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"user","agent":"build"}');
insert into part values('p1','m1','{"type":"text","text":"why does the exporter drop every third retry","time":{"start":"2026-01-02T03:00:00Z"}}');
insert into message values('m2','s1',1767409260000,'{"role":"user","agent":"build"}');
insert into part values('p2','m2','{"type":"text","ignored":true,"text":"▣ DCP | -100.3K removed, +9K summary — Compression #1","time":{"start":"2026-01-02T03:01:00Z"}}');
insert into message values('m3','s1',1767409320000,'{"role":"assistant","agent":"build"}');
insert into part values('p3','m3','{"type":"text","text":"the backoff counts from zero","time":{"start":"2026-01-02T03:02:00Z"}}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	var users []string
	for _, m := range ss[0].Messages {
		if m.Role == "user" {
			users = append(users, m.Text)
		}
	}
	if len(users) != 1 || users[0] != "why does the exporter drop every third retry" {
		t.Errorf("user turns = %q, want the typed prompt alone", users)
	}
	for _, m := range ss[0].Messages {
		if m.Text == "▣ DCP | -100.3K removed, +9K summary — Compression #1" {
			t.Errorf("the banner is indexed under role %q", m.Role)
		}
	}
	// The turn either side of it is untouched: what goes is the banner, not the
	// conversation around it.
	if len(ss[0].Messages) != 2 {
		t.Errorf("messages = %d, want the question and the answer: %#v", len(ss[0].Messages), ss[0].Messages)
	}
}

// A reply whose message and part were created before the watermark and whose
// text landed after it is still asked for, and its session comes back whole —
// the index replaces a session it reads again rather than adding to it (#4207).
func TestOpencodeSinceAsksForAReplyWrittenAfterThePass(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	// The watermark the half-written pass stamped: after the part started,
	// before its text was written.
	mark := time.UnixMilli(1790858251964)
	for _, tc := range []struct {
		name, script string
	}{
		{"1.x", `create table session(id text primary key, directory text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, time_updated integer, data text);
create table part(id text primary key, message_id text, session_id text, time_created integer, time_updated integer, data text);
insert into session values('s1','/w/app',1790858248400,1790858252010);
insert into message values('m1','s1',1790858248489,1790858248489,'{"role":"user"}');
insert into part values('p1','m1','s1',1790858248489,1790858248489,'{"type":"text","text":"Reply ok. Topic: the basaltfinch rollout."}');
insert into message values('m2','s1',1790858248697,1790858252007,'{"role":"assistant"}');
insert into part values('p2','m2','s1',1790858251964,1790858251965,'{"type":"text","text":"ok","time":{"start":1790858251964,"end":1790858251965}}');
insert into session values('quiet','/w/app',1790858000000,1790858000100);
insert into message values('m3','quiet',1790858000000,1790858000000,'{"role":"user"}');
insert into part values('p3','m3','quiet',1790858000000,1790858000000,'{"type":"text","text":"an older session"}');`},
		// A store from before message and part carried time_updated: the
		// session's own stamp is what says it moved.
		{"1.x without row stamps", `create table session(id text primary key, directory text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, data text);
create table part(id text primary key, message_id text, data text);
insert into session values('s1','/w/app',1790858248400,1790858252010);
insert into message values('m1','s1',1790858248489,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"Reply ok. Topic: the basaltfinch rollout."}');
insert into message values('m2','s1',1790858248697,'{"role":"assistant"}');
insert into part values('p2','m2','{"type":"text","text":"ok","time":{"start":1790858251964,"end":1790858251965}}');
insert into session values('quiet','/w/app',1790858000000,1790858000100);
insert into message values('m3','quiet',1790858000000,'{"role":"user"}');
insert into part values('p3','m3','{"type":"text","text":"an older session"}');`},
		{"2.x", `create table session_v2(id text primary key, directory text, time_created integer, time_updated integer);
create table session_message(id text primary key, session_id text, type text, seq integer, time_created integer, time_updated integer, data text);
insert into session_v2 values('s1','/w/app',1790858248400,1790858252010);
insert into session_message values('m1','s1','user',1,1790858248489,1790858248489,'{"text":"Reply ok. Topic: the basaltfinch rollout.","time":{"created":1790858248489}}');
insert into session_message values('m2','s1','assistant',2,1790858248697,1790858252007,'{"content":[{"type":"text","text":"ok","time":{"created":1790858251964}}],"time":{"created":1790858248697}}');
insert into session_v2 values('quiet','/w/app',1790858000000,1790858000100);
insert into session_message values('m3','quiet','user',1,1790858000000,1790858000000,'{"text":"an older session","time":{"created":1790858000000}}');`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "opencode.db")
			if out, err := exec.CommandContext(t.Context(), "sqlite3", db, tc.script).CombinedOutput(); err != nil {
				t.Fatalf("sqlite: %v %s", err, out)
			}
			for _, read := range []struct {
				name  string
				parse func(string, time.Time) ([]model.Session, error)
			}{{"opencode", ParseOpencodeDBSince}, {"kilocode", ParseKiloDBSince}, {"zcode", ParseZCodeDBSince}} {
				ss, err := read.parse(db, mark)
				if err != nil {
					t.Fatalf("%s: %v", read.name, err)
				}
				if len(ss) != 1 || ss[0].ID != "s1" {
					t.Fatalf("%s: want s1 alone, got %d sessions", read.name, len(ss))
				}
				var texts []string
				for _, m := range ss[0].Messages {
					texts = append(texts, m.Role+": "+m.Text)
				}
				if got := strings.Join(texts, " | "); got != "user: Reply ok. Topic: the basaltfinch rollout. | assistant: ok" {
					t.Errorf("%s: the session did not come back whole: %q", read.name, got)
				}
			}
		})
	}
}

// The watermark is a session's own stamp and the comparison is strict, so a row
// stamped in the same millisecond as it was never asked for. The read starts a
// few seconds back; a session read twice replaces itself (#4207).
func TestOpencodeSinceReadsARowStampedAtTheWatermark(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text primary key, directory text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, time_updated integer, data text);
create table part(id text primary key, message_id text, session_id text, time_created integer, time_updated integer, data text);
insert into session values('s1','/w/app',1790858248400,1790858252010);
insert into message values('m1','s1',1790858252010,1790858252010,'{"role":"user"}');
insert into part values('p1','m1','s1',1790858252010,1790858252010,'{"type":"text","text":"same millisecond"}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite: %v %s", err, out)
	}
	ss, err := ParseOpencodeDBSince(db, time.UnixMilli(1790858252010))
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Errorf("a row stamped at the watermark came back in %d sessions", len(ss))
	}
}

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
	ids := make([]string, 0, len(ss))
	for _, s := range ss {
		ids = append(ids, s.ID)
	}
	if len(ids) != 1 || ids[0] != "new" {
		t.Fatalf("since = %v, want only new — a watermark at the migration date must still see it", ids)
	}
}

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
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
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

// A busy sqlite store (another connection holding a write lock) used to make
// sqlite3 exit "database is locked" with empty stdout, so the whole harness's
// history silently vanished from the index run — during active use, when the
// freshest sessions exist. The parser now passes -readonly and .timeout so it
// waits out the lock instead of dropping everything.
func TestOpencodeReadsThroughAWriteLock(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "opencode.db")
	script := `pragma journal_mode=delete;
create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"LOCKEDMARK content"}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}

	// Hold an exclusive write lock via a background sqlite3 fed through stdin,
	// released when we close stdin — after the parser has run.
	holder := exec.CommandContext(t.Context(), "sqlite3", db)
	pipe, err := holder.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = holder.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err = pipe.Write([]byte("begin exclusive;\ninsert into part values('p2','m1','x');\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // let the exclusive lock take hold

	done := make(chan struct{})
	go func() {
		time.Sleep(400 * time.Millisecond)
		_, _ = pipe.Write([]byte("rollback;\n"))
		_ = pipe.Close()
		close(done)
	}()

	ss, err := ParseOpencodeDB(db)
	if err != nil {
		t.Fatalf("parse failed under a write lock: %v", err)
	}
	if len(ss) != 1 {
		t.Fatalf("the locked store's history was dropped: got %d sessions", len(ss))
	}
	<-done
	_ = holder.Wait()
}

// The since clause filters on the message row's column and the reader dated the
// message from the blob, so a row stamped in the column alone came back with no
// time at all — the year zero, published as it is by `show --json` and treated
// as one turn by an index that keys duplicates on (role, time, text) (#2086).
func TestAnOpencodeMessageIsDatedByWhateverCarriesTheStamp(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	started := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	inColumn := time.Date(2026, 1, 1, 12, 30, 0, 0, time.UTC)
	inBlob := time.Date(2026, 1, 1, 13, 0, 0, 0, time.UTC)
	sql := `create table session (id text primary key, directory text, time_created integer, time_updated integer);
create table message (id text primary key, session_id text, data text, time_created integer);
create table part (id text primary key, message_id text, data text);
insert into session values ('s1','/tmp/app',` + ms(started) + `,` + ms(inBlob) + `);
insert into message values ('m1','s1','{"role":"user"}',` + ms(inColumn) + `);
insert into part values ('p1','m1',json_object('type','text','text','the column carries this one'));
insert into message values ('m2','s1','{"role":"user","time":{"created":` + ms(inBlob) + `}}',` + ms(inBlob) + `);
insert into part values ('p2','m2',json_object('type','text','text','the blob carries this one'));
insert into message values ('m3','s1','{"role":"user"}',0);
insert into part values ('p3','m3',json_object('type','text','text','nothing carries this one'));`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 3 {
		t.Fatalf("the fixture did not come back whole, so this measures nothing: %#v", ss)
	}
	want := map[string]time.Time{
		"the column carries this one": inColumn,
		"the blob carries this one":   inBlob,
		// Neither has one, so it belongs to its conversation — the fallback
		// cursor has taken since bubbles without stamps turned up there.
		"nothing carries this one": started,
	}
	for _, m := range ss[0].Messages {
		if got := m.Time.UTC(); !got.Equal(want[m.Text]) {
			t.Errorf("%q is dated %s, want %s", m.Text, got, want[m.Text].UTC())
		}
	}
}

// ms is what opencode writes: epoch milliseconds.
func ms(t time.Time) string { return strconv.FormatInt(t.UnixMilli(), 10) }

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
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
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
		if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
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
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
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

// opencode records a subagent run as its own session with parent_id pointing
// at the spawner — 922 of 1472 sessions on a real store — and the reader never
// read the column, so every child listed as a person's own session (#3301).
func TestOpencodeSubagentSessionKnowsItsParent(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text, parent_id text, directory text, title text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('ses_parent',NULL,'/w','Adversarial analysis','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into session values('ses_child','ses_parent','/w','Review QA screenshots (@general subagent)','2026-01-02T03:10:00Z','2026-01-02T03:20:00Z');
insert into message values('m1','ses_parent',1767409200000,'{"role":"user","agent":"build"}');
insert into part values('p1','m1','{"type":"text","text":"review the screenshots","time":{"start":"2026-01-02T03:00:00Z"}}');
insert into message values('m2','ses_child',1767409800000,'{"role":"user","agent":"general"}');
insert into part values('p2','m2','{"type":"text","text":"Review QA screenshots in /w/shots and list the defects","time":{"start":"2026-01-02T03:10:00Z"}}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 2 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	for _, s := range ss {
		switch s.ID {
		case "ses_child":
			if s.Kind != "subagent" || s.Parent != "ses_parent" {
				t.Errorf("child kind=%q parent=%q, want subagent / ses_parent", s.Kind, s.Parent)
			}
		case "ses_parent":
			if s.Kind != "" || s.Parent != "" {
				t.Errorf("parent kind=%q parent=%q, want none", s.Kind, s.Parent)
			}
		}
	}
}

// opencode's apply_patch resolves a relative header against the session's
// directory; gpt models write "*** Add File: notes.txt" often enough that a
// sub-agent's whole change was filed under a bare name blame never matched.
func TestOpencodeRelativePatchPathsResolveAgainstTheSessionDirectory(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	patch := `*** Begin Patch\n*** Add File: notes.txt\n+the sub agent wrote this particular line of text.\n` +
		`*** Update File: ./sub/app.go\n@@\n-func oldName() error { return nil }\n+func newName() error { return errors.New(\"x\") }\n` +
		`*** Update File: /elsewhere/abs.go\n@@\n-an absolute path stays exactly as it was\n+and so does its replacement line here\n*** End Patch`
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w/app2','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"assistant"}');
insert into part values('p1','m1','{"type":"tool","tool":"apply_patch","state":{"status":"completed","input":{"patchText":"` + patch + `"}},"time":{"start":"2026-01-02T03:00:02Z"}}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	var wrote, edits []string
	for _, m := range ss[0].Messages {
		head, _, _ := strings.Cut(m.Text, "\n")
		switch m.Role {
		case RoleWrote:
			wrote = append(wrote, head)
		case RoleEdit:
			edits = append(edits, head)
		}
	}
	if want := []string{"/w/app2/notes.txt", "/w/app2/sub/app.go", "/elsewhere/abs.go"}; strings.Join(wrote, ",") != strings.Join(want, ",") {
		t.Errorf("wrote records name %v, want %v", wrote, want)
	}
	if want := []string{"/w/app2/sub/app.go", "/elsewhere/abs.go"}; strings.Join(edits, ",") != strings.Join(want, ",") {
		t.Errorf("edit spans name %v, want %v", edits, want)
	}
}

// A part whose text is full of quotes and newlines — an agent pasting JSON, or
// a test run printing diffs — is the ordinary case on a real store, and it used
// to be the case that never finished. The sqlite3 shell's own -json formatter
// is quadratic in the number of characters it escapes: 4 MB of text with a
// quote every sixteenth byte takes it 412 seconds against 0.04 with
// json_object, and an index run over a store like that never returned (#3553).
func TestOpencodeReadsAQuoteHeavyPartWithoutStalling(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "opencode.db")
	// The bulk is built inside SQLite so the fixture stays a few hundred bytes:
	// 4 MB of text carrying ~250,000 quotes, with a needle in front holding
	// every character JSON has to escape.
	needle := `he said "no" \ then` + "\n\ttab and ⌘"
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"assistant"}');
insert into part select 'p1','m1',json_object(
  'type','text',
  'text',` + sqlQuote(needle) + `||replace(hex(randomblob(2000000)),'A','"'),
  'time',json_object('start','2026-01-02T03:00:00Z'));`
	sql := filepath.Join(tmp, "setup.sql")
	if err := os.WriteFile(sql, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	setup := exec.CommandContext(t.Context(), "sqlite3", db)
	f, err := os.Open(sql)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	setup.Stdin = f
	if out, err := setup.CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}

	type result struct {
		ss  []model.Session
		err error
	}
	done := make(chan result, 1)
	go func() {
		ss, err := ParseOpencodeDB(db)
		done <- result{ss, err}
	}()
	var got result
	select {
	case got = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("reading one 4 MB part took over 30 seconds — the escaping path is quadratic again")
	}
	if got.err != nil || len(got.ss) != 1 {
		t.Fatalf("len=%d err=%v", len(got.ss), got.err)
	}
	var text string
	for _, m := range got.ss[0].Messages {
		if m.Role == "assistant" {
			text = m.Text
		}
	}
	if !strings.HasPrefix(text, needle) {
		t.Fatalf("text = %.80q — quotes, backslashes, tabs, newlines and non-ASCII all have to survive the framing", text)
	}
}

// sqlQuote renders a Go string as a SQL literal for the fixture above.
func sqlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// What sqlite3 says when it refuses, not merely that it did. A person whose
// store deja would not read was told "its format may have changed; please
// report it", and the report they could send said `exit status 1` — which names
// neither a renamed column nor a locked database nor a file that is not a
// database at all (#1642).
func TestAStoreRefusalCarriesWhatSqliteSaid(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("no sqlite3")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	// A real database missing the column the first probe reads, which is the
	// shape a schema change arrives in. The probe asks message too, for the
	// newest session with turns, so that table is there.
	mk := exec.CommandContext(t.Context(), "sqlite3", db, "create table session(id text, directory text); create table message(id text, session_id text);")
	if out, err := mk.CombinedOutput(); err != nil {
		t.Fatalf("could not build the fixture: %v: %s", err, out)
	}

	_, err := ParseOpencodeNewest(db)
	if err == nil {
		t.Fatal("a store with no time_created column parsed without complaint")
	}
	msg := err.Error()
	if !strings.Contains(msg, "no such column") {
		t.Errorf("the refusal does not say what sqlite3 said: %q", msg)
	}
	if msg == "exit status 1" {
		t.Errorf("the reason is still the exit code alone: %q", msg)
	}
}

// The quoted complaint is one line and bounded: sqlite3 echoes the statement it
// failed on, and that statement is a screenful.
func TestTheQuotedComplaintIsBounded(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("Error: in prepare, no such column: time_created " +
		strings.Repeat("select s.id, ", 60) + "\nsecond line")
	got := withStderr(errors.New("exit status 1"), &buf).Error()
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("the reason spans lines: %q", got)
	}
	if len(got) > sqliteStderrMax+40 {
		t.Errorf("the reason is %d bytes, the bound is %d", len(got), sqliteStderrMax)
	}
	if !strings.HasPrefix(got, "Error: in prepare, no such column: time_created") {
		t.Errorf("the head, which names the cause, was cut: %q", got)
	}
}

// Nothing on stderr means nothing to add: the caller keeps the error it had.
func TestASilentFailureKeepsItsOwnError(t *testing.T) {
	var empty bytes.Buffer
	want := errors.New("exit status 1")
	if got := withStderr(want, &empty); got != want { //nolint:errorlint // the very same error value must come back
		t.Errorf("a silent failure was rewritten: %v", got)
	}
	_ = os.Stdout
}

// Resume asks the store for the row itself (#4205): every session either
// table holds counts, whatever the projection would read from it.
func TestOpencodeStoreLacksAsksForTheRow(t *testing.T) {
	db := opencodeMixedFixture(t)
	t.Setenv("DEJA_OPENCODE_DB", db)
	for _, id := range []string{"old", "moved", "new"} {
		if OpencodeStoreLacks("opencode", id) {
			t.Errorf("%s is in the store and was called deleted", id)
		}
	}
	if !OpencodeStoreLacks("opencode", "gone") {
		t.Error("an id in neither table was not called deleted")
	}
	if !OpencodeStoreLacks("opencode", "it's") {
		t.Error("a quoted id broke the lookup")
	}
	// A session with no turns is still opencode's.
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, `insert into session_v2 values('empty','p1',null,'/w','',1,1)`).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if OpencodeStoreLacks("opencode", "empty") {
		t.Error("an empty session was called deleted")
	}

	// Kilo's CLI database is asked the same way.
	t.Setenv("DEJA_KILO_DB", db)
	if OpencodeStoreLacks("kilocode", "new") || !OpencodeStoreLacks("kilocode", "gone") {
		t.Error("the Kilo store was not the one asked")
	}
	// No store: nothing to go on.
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "missing.db"))
	if OpencodeStoreLacks("opencode", "gone") {
		t.Error("refused with no store to ask")
	}
}

// opencode marks its own digest of a compacted conversation on the message —
// `summary: true` — and deja read it as the agent talking: 1,906 of them on one
// store, 19.6% of everything indexed in the sessions that have them. It is kept,
// because it is the only record of the turns that went away, and it is kept
// under its own role, so an ordinary question does not reach it (#3384).
func TestOpencodeCompactionSummaryIsNotSpeech(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"user","agent":"build"}');
insert into part values('p1','m1','{"type":"text","text":"why does the exporter drop every third retry","time":{"start":"2026-01-02T03:00:00Z"}}');
insert into message values('m2','s1',1767409260000,'{"role":"assistant","agent":"build"}');
insert into part values('p2','m2','{"type":"text","text":"the backoff counts from zero","time":{"start":"2026-01-02T03:01:00Z"}}');
insert into message values('m3','s1',1767409320000,'{"role":"assistant","summary":true,"mode":"compaction","agent":"compaction"}');
insert into part values('p3','m3','{"type":"text","text":"## Goal\nBring the exporter to a state where the retry budget is three","time":{"start":"2026-01-02T03:02:00Z"}}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	roles := make([]string, 0, len(ss[0].Messages))
	var summary string
	for _, m := range ss[0].Messages {
		roles = append(roles, m.Role)
		if m.Role == RoleSummary {
			summary = m.Text
		}
	}
	if summary == "" {
		t.Fatalf("the summary was dropped rather than filed under its own role: %v", roles)
	}
	for _, m := range ss[0].Messages {
		if m.Role == "assistant" && m.Text == summary {
			t.Error("the summary is still indexed as the agent's speech")
		}
	}
	// The conversation around it is untouched: what changed is the attribution
	// of one message, not what the session holds.
	if len(ss[0].Messages) != 3 {
		t.Errorf("messages = %d, want the question, the answer and the summary: %v", len(ss[0].Messages), roles)
	}
}

// The value behind that flag is an object of file diffs on current opencode:
// 247 MB of query output against 132 MB for the same 78,690 rows on a 3.4 GB
// store, for a boolean an object can never satisfy (#3556). Nothing in the
// projection may ask for it again.
func TestTheCompactionFlagIsReadByTypeNotByValue(t *testing.T) {
	b, err := os.ReadFile("opencode.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `'summary',json_extract(`) {
		t.Error("the projection carries the summary value again — json_type answers the same question for nothing")
	}
}

// The compaction flag is read by type rather than by value, because current
// opencode writes an object of file diffs under the same key and it runs to
// megabytes (#3556). Every shape that used to mark a message as opencode's own
// digest still does, and the object still does not.
func TestOpencodeReadsTheCompactionFlagWhateverShapeItIsIn(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	for _, tc := range []struct {
		name, summary string
		wantSummary   bool
	}{
		{name: "the boolean opencode writes", summary: `true`, wantSummary: true},
		{name: "a store that writes one", summary: `1`, wantSummary: true},
		{name: "a store that writes the word", summary: `"true"`, wantSummary: true},
		{name: "false is not a digest", summary: `false`},
		{name: "zero is not a digest", summary: `0`},
		{name: "a diff object is not a digest", summary: `{"diffs":{"/w/app.go":"@@ -1 +1 @@"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			db := filepath.Join(tmp, "opencode.db")
			writeStore(t, db, `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"assistant","summary":`+tc.summary+`}');
insert into part values('p1','m1','{"type":"text","text":"what the pass carried over","time":{"start":"2026-01-02T03:00:00Z"}}');`)
			ss, err := ParseOpencodeDB(db)
			if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 {
				t.Fatalf("len=%d err=%v", len(ss), err)
			}
			got := ss[0].Messages[0].Role
			if (got == RoleSummary) != tc.wantSummary {
				t.Fatalf("role = %q, want summary = %v", got, tc.wantSummary)
			}
			if !strings.Contains(ss[0].Messages[0].Text, "carried over") {
				t.Fatalf("text = %q", ss[0].Messages[0].Text)
			}
		})
	}
}

// opencode marks the text it writes under the user role itself with
// synthetic: true — "Continue if you have next steps…", "The following tool
// was executed by the user" — and 338 of them indexed as the person's words on
// a real store (#3299).
func TestOpencodeSyntheticPartsAreNotThePersons(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"user","agent":"build"}');
insert into part values('p1','m1','{"type":"text","text":"why does the retry loop drop the last attempt","time":{"start":"2026-01-02T03:00:00Z"}}');
insert into message values('m2','s1',1767409260000,'{"role":"assistant","agent":"build"}');
insert into part values('p2','m2','{"type":"text","text":"the deadline is reused","time":{"start":"2026-01-02T03:01:00Z"}}');
insert into message values('m3','s1',1767409320000,'{"role":"user","agent":"build"}');
insert into part values('p3','m3','{"type":"text","synthetic":true,"text":"Continue if you have next steps, or stop and ask for clarification if you are unsure how to proceed.","time":{"start":"2026-01-02T03:02:00Z"}}');
insert into message values('m4','s1',1767409380000,'{"role":"user","agent":"build"}');
insert into part values('p4','m4','{"type":"text","synthetic":true,"text":"The following tool was executed by the user","time":{"start":"2026-01-02T03:03:00Z"}}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	var users []string
	for _, m := range ss[0].Messages {
		if m.Role == "user" {
			users = append(users, m.Text)
		}
	}
	if len(users) != 1 || users[0] != "why does the retry loop drop the last attempt" {
		t.Errorf("user turns = %q, want the typed prompt alone", users)
	}
}

func TestPatchSpansTakesTheRemovedLines(t *testing.T) {
	patch := "*** Begin Patch\n" +
		"*** Update File: /w/app.go\n" +
		"@@\n" +
		"-func old() error {\n" +
		"-\treturn nil\n" +
		"+func nw() error {\n" +
		"+\treturn errors.New(\"x\")\n" +
		"*** End Patch"
	got := patchSpans(patch)
	if len(got) != 1 {
		t.Fatalf("got %d spans, want one", len(got))
	}
	path, body, _ := strings.Cut(got[0], "\n")
	if path != "/w/app.go" {
		t.Fatalf("path = %q", path)
	}
	if body != "func old() error {\n\treturn nil" {
		t.Fatalf("body = %q — only the removed lines belong in a span", body)
	}
}

func TestPatchSpansHandlesSeveralFilesAndNoRemovals(t *testing.T) {
	patch := "*** Begin Patch\n" +
		"*** Update File: /w/a.go\n@@\n-one\n+two\n" +
		"*** Add File: /w/b.go\n+brand new\n" +
		"*** Update File: /w/c.go\n@@\n-three\n" +
		"*** End Patch"
	got := patchSpans(patch)
	if len(got) != 2 {
		t.Fatalf("got %v — a file with nothing removed has no span to keep", got)
	}
	if !strings.HasPrefix(got[0], "/w/a.go\n") || !strings.HasPrefix(got[1], "/w/c.go\n") {
		t.Fatalf("got %v", got)
	}
	if len(patchSpans("")) != 0 || len(patchSpans("nonsense")) != 0 {
		t.Fatal("junk in, nothing out")
	}
}

func TestWorthIndexingCoversMoreThanOneEcosystem(t *testing.T) {
	for _, cmd := range []string{
		"go test ./internal/index/ -run Posting",
		"git status --short",
		"latexmk -pdf thesis.tex",
		"uv run pytest -q",
		"gradle build",
		"psql -c 'select 1'",
	} {
		if !worthIndexing(cmd) {
			t.Errorf("%q is work worth recording", cmd)
		}
	}
	for _, cmd := range []string{
		"ls -la",
		"cd /tmp",
		"cat file.txt",
		"python3 - <<'PY'\nprint(1)\nPY",
	} {
		if worthIndexing(cmd) {
			t.Errorf("%q is not", cmd)
		}
	}
}

// TestOpencodeParsesCommandsAndPatches drives the row branches added for bash
// and apply_patch through the real SQL path, since a unit test of patchSpans
// alone would leave the query and the row loop unmeasured.
func TestOpencodeParsesCommandsAndPatches(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "opencode.db")
	patch := `*** Begin Patch\n*** Update File: /w/app.go\n@@\n-func old() error {\n+func nw() error {\n*** End Patch`
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"assistant"}');
insert into part values('p1','m1','{"type":"tool","tool":"bash","state":{"input":{"command":"go test ./pkg/ -run Retry"}},"time":{"start":"2026-01-02T03:00:00Z"}}');
insert into part values('p2','m1','{"type":"tool","tool":"bash","state":{"input":{"command":"ls -la"}},"time":{"start":"2026-01-02T03:00:01Z"}}');
insert into part values('p3','m1','{"type":"tool","tool":"apply_patch","state":{"input":{"patchText":"` + patch + `"}},"time":{"start":"2026-01-02T03:00:02Z"}}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	var cmds, edits []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleCommand:
			cmds = append(cmds, m.Text)
		case RoleEdit:
			edits = append(edits, m.Text)
		}
	}
	if len(cmds) != 1 || cmds[0] != "$ go test ./pkg/ -run Retry" {
		t.Fatalf("commands = %v — `ls` is not work worth recording", cmds)
	}
	if len(edits) != 1 || !strings.HasPrefix(edits[0], "/w/app.go\n") ||
		!strings.Contains(edits[0], "func old() error {") {
		t.Fatalf("edits = %v", edits)
	}
}

func TestOpencodeIndexesCommandOutputAndExitStatus(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "opencode.db")
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"assistant"}');
insert into part values('p1','m1','{"type":"tool","tool":"bash","state":{"input":{"command":"go test ./pkg/"},"output":"--- FAIL: TestRetry\nFAIL\tpkg 0.1s","metadata":{"exit":1}},"time":{"start":"2026-01-02T03:00:00Z"}}');
insert into part values('p2','m1','{"type":"tool","tool":"bash","state":{"input":{"command":"go build ./..."},"output":"","metadata":{"exit":0}},"time":{"start":"2026-01-02T03:00:01Z"}}');
insert into part values('p3','m1','{"type":"tool","tool":"read","state":{"input":{"filePath":"/w/big.go"},"output":"package main\n// thousands of lines of file content"},"time":{"start":"2026-01-02T03:00:02Z"}}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	var cmds, outs []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleCommand:
			cmds = append(cmds, m.Text)
		case RoleToolOutput:
			outs = append(outs, m.Text)
		}
	}
	// Both failure and explicit exit 0 carry their recorded status.
	if len(cmds) != 2 {
		t.Fatalf("commands = %v", cmds)
	}
	if !strings.Contains(cmds[0], "→ exit 1") {
		t.Errorf("a non-zero exit belongs on the command: %q", cmds[0])
	}
	if !strings.HasSuffix(cmds[1], "  → exit 0") {
		t.Errorf("an explicit clean exit belongs on the command: %q", cmds[1])
	}
	// The output of a command is indexed; the body of a file read is not —
	// file contents are 119 MB against 49 MB of command output on a real store.
	if len(outs) != 1 || !strings.Contains(outs[0], "--- FAIL") {
		t.Fatalf("tool output = %v", outs)
	}
	for _, o := range outs {
		if strings.Contains(o, "thousands of lines") {
			t.Error("read bodies must not be indexed as tool output")
		}
	}
}

func TestExitCodeReadsEveryShapeSQLiteEmits(t *testing.T) {
	for in, want := range map[any]int{
		float64(1): 1, float64(0): 0, "127": 127, "": 0, nil: 0, "junk": 0,
	} {
		if got := exitCode(in); got != want {
			t.Errorf("exitCode(%#v) = %d, want %d", in, got, want)
		}
	}
}

// opencode names every session; the reader ignored the column and titled a
// session by its first user line, which for a subagent run is the whole brief
// (#3315). A thin name — "done", "ok", "Greeting", 28 of 1472 on a real store —
// is left to the index, as Continue's placeholders are (#3274).
func TestOpencodeTakesTheSessionTitleUnlessItIsThin(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text, parent_id text, directory text, title text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('ses_named',NULL,'/w','Review route overlap fix (@code-reviewer subagent)','2026-01-02T03:00:00Z','2026-01-02T03:30:00Z');
insert into message values('m1','ses_named',1767409200000,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"Read-only review of the current uncommitted NetGateway route overlap change","time":{"start":"2026-01-02T03:00:00Z"}}');
insert into session values('ses_thin',NULL,'/w','done','2026-01-02T04:00:00Z','2026-01-02T04:30:00Z');
insert into message values('m2','ses_thin',1767412800000,'{"role":"user"}');
insert into part values('p2','m2','{"type":"text","text":"render the plots for the report","time":{"start":"2026-01-02T04:00:00Z"}}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 2 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	got := map[string]string{}
	for _, s := range ss {
		got[s.ID] = s.Title
	}
	if got["ses_named"] != "Review route overlap fix (@code-reviewer subagent)" {
		t.Errorf("named session titled %q", got["ses_named"])
	}
	if got["ses_thin"] != "" {
		t.Errorf("a thin name was taken as the title: %q", got["ses_thin"])
	}
}

// opencodeV2Fixture writes a store in opencode 2.x's schema, with the shapes a
// live 2.0.12 database carries (#3924): a user turn keeping its text at the top
// of `data`, an assistant turn holding text and tool parts under `$.content`, a
// tool naming itself under `$.name`, and a command's output in the block list
// under `$.state.content`.
func opencodeV2Fixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	patch := `*** Begin Patch\n*** Update File: /w/app.go\n@@\n-func old() error {\n+func nw() error {\n*** End Patch`
	script := `create table session_v2(id text primary key, project_id text, parent_id text, directory text, title text, time_created integer, time_updated integer);
create table session_message(id text primary key, session_id text, type text, seq integer, time_created integer, time_updated integer, data text);
insert into session_v2 values('s1','p1',null,'/w','retry flake in the payments suite',1767409200000,1767495600000);
insert into session_message values('m1','s1','user',1,1767409200000,1767409200000,'{"metadata":{},"time":{"created":1767409200000},"text":"why does TestRetry flake"}');
insert into session_message values('m2','s1','assistant',2,1767409201000,1767409201000,'{"time":{"created":1767409201000},"content":[` +
		`{"type":"text","text":"the timeout is too short"},` +
		`{"type":"reasoning","text":"thinking about it"},` +
		`{"type":"tool","id":"call_1","name":"bash","state":{"status":"completed","input":{"command":"go test ./pkg/ -run Retry"},"content":[{"type":"text","text":"--- FAIL: TestRetry"}],"metadata":{"exit":1}},"time":{"start":1767409202000}},` +
		`{"type":"tool","id":"call_2","name":"bash","state":{"status":"completed","input":{"command":"ls -la"},"content":[],"metadata":{"exit":0}},"time":{"start":1767409203000}},` +
		`{"type":"tool","id":"call_3","name":"read","state":{"status":"completed","input":{"filePath":"/w/retry.go"},"content":[{"type":"text","text":"package main // thousands of lines"}]},"time":{"start":1767409204000}},` +
		`{"type":"tool","id":"call_4","name":"apply_patch","state":{"status":"completed","input":{"patchText":"` + patch + `"}},"time":{"start":1767409205000}}` +
		`]}');
insert into session_message values('m3','s1','synthetic',3,1767409206000,1767409206000,'{"time":{"created":1767409206000},"text":"Continue if you have next steps"}');
insert into session_message values('m4','s1','model-switched',4,1767409207000,1767409207000,'{"time":{"created":1767409207000},"text":"switched to another model"}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	return db
}

func TestOpencodeReadsTheV2Schema(t *testing.T) {
	db := opencodeV2Fixture(t)
	ss, err := ParseOpencodeDB(db)
	if err != nil {
		t.Fatalf("a 2.x store must parse, not report the schema missing: %v", err)
	}
	if len(ss) != 1 {
		t.Fatalf("got %d sessions, want the one the store holds", len(ss))
	}
	s := ss[0]
	if s.Project != projectName("/w") || s.Path != "/w" {
		t.Errorf("project = %q path = %q — session_v2.directory still names the project", s.Project, s.Path)
	}
	if s.Title != "retry flake in the payments suite" {
		t.Errorf("title = %q", s.Title)
	}
	var user, assistant, cmds, outs, files, edits []string
	for _, m := range s.Messages {
		switch m.Role {
		case "user":
			user = append(user, m.Text)
		case "assistant":
			assistant = append(assistant, m.Text)
		case RoleCommand:
			cmds = append(cmds, m.Text)
		case RoleToolOutput:
			outs = append(outs, m.Text)
		case RoleFiles:
			files = append(files, m.Text)
		case RoleEdit:
			edits = append(edits, m.Text)
		}
	}
	// The role lives in the message's `type` column now; without reading it
	// every turn would come back as the same speaker.
	if len(user) != 1 || user[0] != "why does TestRetry flake" {
		t.Errorf("user turns = %v", user)
	}
	if len(assistant) != 1 || assistant[0] != "the timeout is too short" {
		t.Errorf("assistant turns = %v", assistant)
	}
	if len(cmds) != 1 || !strings.HasPrefix(cmds[0], "$ go test ./pkg/ -run Retry") {
		t.Fatalf("commands = %v — `ls` is not work worth recording", cmds)
	}
	if !strings.Contains(cmds[0], "→ exit 1") {
		t.Errorf("a non-zero exit belongs on the command: %q", cmds[0])
	}
	// `$.state.output` became the block list `$.state.content`.
	if len(outs) != 1 || !strings.Contains(outs[0], "--- FAIL: TestRetry") {
		t.Errorf("tool output = %v", outs)
	}
	for _, o := range outs {
		if strings.Contains(o, "thousands of lines") {
			t.Error("a file body is not command output, on either schema")
		}
	}
	if len(files) != 1 || files[0] != "/w/retry.go" {
		t.Errorf("files = %v", files)
	}
	if len(edits) != 1 || !strings.HasPrefix(edits[0], "/w/app.go\n") {
		t.Errorf("edits = %v", edits)
	}
	// opencode's own turns — the "Continue if you have next steps" line and the
	// switch notices — are not the person talking. They carried `$.synthetic`
	// on the old schema and are their own message types here.
	for _, m := range s.Messages {
		if strings.Contains(m.Text, "Continue if you have next steps") ||
			strings.Contains(m.Text, "switched to another model") {
			t.Errorf("%s: %q is opencode talking to itself", m.Role, m.Text)
		}
	}
	if s.Started.IsZero() || s.Updated.IsZero() {
		t.Errorf("started = %v updated = %v — the epoch milliseconds still date the session", s.Started, s.Updated)
	}
}

func TestOpencodeV2StoreAnswersTheReadsBesideTheProjection(t *testing.T) {
	db := opencodeV2Fixture(t)
	// doctor asks this one first, and on a 2.x store it asked `session`, which
	// is the query that reported the whole harness unreadable (#3924).
	ss, err := ParseOpencodeNewest(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("newest: len=%d err=%v", len(ss), err)
	}
	if got := opencodeSessionTable(db); got != "session_v2" {
		t.Fatalf("session table = %q", got)
	}
	if titles := opencodeTitles(db); titles["s1"] != "retry flake in the payments suite" {
		t.Errorf("titles = %v", titles)
	}
}

func TestOpencodeStillReadsTheV1Schema(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"assistant"}');
insert into part values('p1','m1','{"type":"text","text":"the timeout is too short","time":{"start":"2026-01-02T03:00:00Z"}}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	if opencodeV2(db) {
		t.Fatal("a 1.x store must not be read as 2.x")
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
}

// The dev build of 2026-09-21 creates `session_message` and leaves it empty
// while the turns stay in `message` and `part`. Reading such a store the 2.x
// way returns nothing at all, and nothing is the answer that made this bug hard
// to see: the store looks healthy and holds no sessions.
func TestOpencodeReadsTheOldTablesWhileTheNewOneIsEmpty(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text primary key, directory text, title text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
create table session_message(id text primary key, session_id text, type text, seq integer, time_created integer, time_updated integer, data text);
insert into session values('s1','/w','the payments suite','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"assistant"}');
insert into part values('p1','m1','{"type":"text","text":"the timeout is too short","time":{"start":"2026-01-02T03:00:00Z"}}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	if opencodeV2(db) {
		t.Fatal("an empty session_message is a store that has not moved yet")
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("len=%d err=%v — the turns are still in message and part", len(ss), err)
	}
}

// Upstream keeps the sessions in `session` and moves the turns into
// `session_message`; the store in #3924 had `session_v2` and no `session`. Both
// are read, and what decides is where the turns are.
func TestOpencodeReadsTheNewTurnsBesideTheOldSessionTable(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text primary key, directory text, title text, time_created integer, time_updated integer);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
create table session_message(id text primary key, session_id text, type text, seq integer, time_created integer, time_updated integer, data text);
insert into session values('s1','/w','the payments suite',1767409200000,1767409300000);
insert into session_message values('m1','s1','user',1,1767409200000,1767409200000,'{"time":{"created":1767409200000},"text":"why does TestRetry flake"}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	if !opencodeV2(db) {
		t.Fatal("turns in session_message are a 2.x store whatever the sessions table is called")
	}
	if got := opencodeSessionTable(db); got != "session" {
		t.Fatalf("session table = %q — session_v2 is the name only where there is no session", got)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	if ss[0].Messages[0].Text != "why does TestRetry flake" {
		t.Fatalf("message = %q", ss[0].Messages[0].Text)
	}
}

func TestOpencodeV2SinceReadsOnlyWhatIsNewer(t *testing.T) {
	db := opencodeV2Fixture(t)
	// The fixture's turns are stamped 2026-01-02; a watermark after them leaves
	// nothing to read, and one before them brings the session back.
	after, err := ParseOpencodeDBSince(db, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("got %d sessions — nothing in the store is newer than the watermark", len(after))
	}
	before, err := ParseOpencodeDBSince(db, time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || len(before) != 1 {
		t.Fatalf("len=%d err=%v", len(before), err)
	}
}

// The names inside a turn come from opencode's own schema (packages/schema
// session-message.ts, packages/core tool/*.ts), not from the report: the file a
// tool opened is `path` where the old parts wrote `filePath`, a command's exit
// status is in the tool's structured output where the old parts wrote
// `state.metadata.exit`, and a compaction carries its summary on the message.
func TestOpencodeV2ReadsTheUpstreamFieldNames(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text primary key, directory text, title text, time_created integer, time_updated integer);
create table session_message(id text primary key, session_id text, type text, seq integer, time_created integer, time_updated integer, data text);
insert into session values('s1','/w','the payments suite',1767409200000,1767409300000);
insert into session_message values('m1','s1','assistant',1,1767409201000,1767409201000,'{"time":{"created":1767409201000},"content":[` +
		`{"type":"tool","id":"c1","name":"read","state":{"status":"completed","input":{"path":"/w/retry.go"},"content":[{"type":"text","text":"package main"}],"structured":{}},"time":{"created":1767409201000}},` +
		`{"type":"tool","id":"c2","name":"bash","state":{"status":"completed","input":{"command":"go test ./pkg/ -run Retry"},"content":[{"type":"text","text":"--- FAIL: TestRetry"}],"structured":{"exit":2,"truncated":false}},"time":{"created":1767409202000}}` +
		`]}');
insert into session_message values('m2','s1','compaction',2,1767409203000,1767409203000,'{"time":{"created":1767409203000},"reason":"auto","summary":"we pinned pgx 5.4.3 for the pgbouncer issue","recent":""}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	var files, cmds, summaries []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleFiles:
			files = append(files, m.Text)
		case RoleCommand:
			cmds = append(cmds, m.Text)
		case RoleSummary:
			summaries = append(summaries, m.Text)
		}
	}
	if len(files) != 1 || files[0] != "/w/retry.go" {
		t.Errorf("files = %v — the new read tool names it `path`", files)
	}
	if len(cmds) != 1 || !strings.Contains(cmds[0], "→ exit 2") {
		t.Errorf("commands = %v — the status is in the tool's structured output", cmds)
	}
	if len(summaries) != 1 || !strings.Contains(summaries[0], "pgx 5.4.3") {
		t.Errorf("summaries = %v — a compaction is the only record of what it compacted", summaries)
	}
}

// The rows of a real 2.0.12 store, written by opencode itself in a throwaway
// HOME: the tools are `read`, `shell` and `edit` — `bash` and `apply_patch`
// were renamed — the file is `path`, the exit status is on the tool's
// metadata, and an edit carries both sides of the change.
func TestOpencodeReadsAnOpencode2Store(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session_v2(id text primary key, project_id text, parent_id text, directory text, title text, time_created integer, time_updated integer);
create table session_message(id text primary key, session_id text, type text, seq integer, time_created integer, time_updated integer, data text);
insert into session_v2 values('s1','p1',null,'/w','Modify add to return a+b+1',1790102970000,1790102990000);
insert into session_message values('m1','s1','user',1,1790102971000,1790102971000,'{"time":{"created":1790102971000},"text":"edit main.go so add returns a+b+1, then run go vet"}');
insert into session_message values('m2','s1','assistant',2,1790102977894,1790102982344,'{"time":{"created":1790102977894},"agent":"build","content":[` +
		`{"type":"reasoning","text":"go vet failed because there is no go module"},` +
		`{"type":"tool","id":"c1","name":"read","executed":false,"state":{"status":"completed","input":{"path":"main.go"},"content":[{"type":"text","text":"package main"}]},"time":{"created":1790102978000}},` +
		`{"type":"tool","id":"c2","name":"edit","executed":false,"state":{"status":"completed","input":{"path":"/w/main.go","oldString":"func add(a, b int) int { return a + b }","newString":"func add(a, b int) int { return a + b + 1 }"},"content":[{"type":"text","text":"Edited main.go (1 replacement)"}]},"time":{"created":1790102979000}},` +
		`{"type":"tool","id":"c3","name":"shell","state":{"status":"completed","input":{"command":"go vet ./..."},"content":[{"type":"text","text":"pattern ./...: directory prefix . does not contain main module"}],"metadata":{"exit":1}},"time":{"created":1790102980000}},` +
		`{"type":"text","text":"add now returns a+b+1"}` +
		`]}');
insert into session_message values('m3','s1','idle',3,1790102983000,1790102983000,'{"time":{"created":1790102983000}}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	got := map[string][]string{}
	for _, m := range ss[0].Messages {
		got[m.Role] = append(got[m.Role], m.Text)
	}
	if len(got["user"]) != 1 || len(got["assistant"]) != 1 {
		t.Errorf("turns = %v / %v", got["user"], got["assistant"])
	}
	if len(got[RoleFiles]) != 1 || got[RoleFiles][0] != "main.go" {
		t.Errorf("files = %v — `read` names the file under `path` now", got[RoleFiles])
	}
	if len(got[RoleCommand]) != 1 || !strings.Contains(got[RoleCommand][0], "go vet ./...") ||
		!strings.Contains(got[RoleCommand][0], "→ exit 1") {
		t.Errorf("commands = %v — `bash` is `shell` here, and its status is on the metadata", got[RoleCommand])
	}
	if len(got[RoleToolOutput]) != 1 || !strings.Contains(got[RoleToolOutput][0], "does not contain main module") {
		t.Errorf("tool output = %v", got[RoleToolOutput])
	}
	// `edit` is 2.0's editing tool, and it hands back what it replaced and what
	// it wrote — both sides, the way every other harness's edit is recorded.
	if len(got[RoleEdit]) != 1 || !strings.HasPrefix(got[RoleEdit][0], "/w/main.go\n") ||
		!strings.Contains(got[RoleEdit][0], "return a + b }") {
		t.Errorf("edits = %v", got[RoleEdit])
	}
	if len(got[RoleWrote]) != 1 || !strings.HasPrefix(got[RoleWrote][0], "/w/main.go\n") {
		t.Errorf("wrote = %v", got[RoleWrote])
	}
}

func TestOpencodeReadsOldTurnsWhenSessionMessageOnlyHasEvents(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text primary key, directory text, title text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, data text);
create table part(id text primary key, message_id text, data text);
create table session_message(id text primary key, session_id text, type text, seq integer, time_created integer, time_updated integer, data text);
insert into session values('s1','/w','transitional opencode schema',1767409200000,1767409300000);
insert into message values('m1','s1',1767409201000,'{"role":"user","time":{"created":1767409201000}}');
insert into part values('p1','m1','{"type":"text","text":"why does TestRetry flake","time":{"start":1767409201000}}');
insert into message values('m2','s1',1767409202000,'{"role":"assistant","time":{"created":1767409202000}}');
insert into part values('p2','m2','{"type":"text","text":"the timeout is too short","time":{"start":1767409202000}}');
insert into session_message values('e1','s1','model-switched',1,1767409200500,1767409200500,'{}');
insert into session_message values('e2','s1','agent-switched',2,1767409201500,1767409201500,'{}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	if opencodeV2(db) {
		t.Fatal("switch events are not conversation turns")
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	if ss[0].Messages[0].Role != "user" || ss[0].Messages[1].Role != "assistant" {
		t.Fatalf("roles = %q, %q", ss[0].Messages[0].Role, ss[0].Messages[1].Role)
	}
}

// Kilo CLI keeps sessions in `session` and almost every turn in message and
// part, while session_message holds switch events and the few turns sent
// through its /api/session route, and there is no session_v2 (#4694). Read
// only through session_message, every other session was dropped. s3 was
// started through the API and continued with `kilo run -s`, so its turns are
// split across both layouts, the API one first.
func TestOpencodeReadsOldTurnsWhenSessionMessageHasAFewTurns(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "kilo.db")
	script := `create table session(id text primary key, directory text, title text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, data text);
create table part(id text primary key, message_id text, data text);
create table session_message(id text primary key, session_id text, type text, seq integer, time_created integer, time_updated integer, data text);
insert into session values('s1','/w','turns in message and part',1767409200000,1767409300000);
insert into message values('m1','s1',1767409201000,'{"role":"user","time":{"created":1767409201000}}');
insert into part values('p1','m1','{"type":"text","text":"why does TestRetry flake","time":{"start":1767409201000}}');
insert into session_message values('e1','s1','agent-switched',1,1767409200500,1767409200500,'{}');
insert into session values('s2','/w','turns in session_message',1767409400000,1767409500000);
insert into session_message values('t2','s2','user',1,1767409401000,1767409401000,'{"time":{"created":1767409401000},"text":"what changed in the retry policy"}');
insert into session values('s3','/w','split across both layouts',1767409600000,1767409700000);
insert into session_message values('t3','s3','user',1,1767409601000,1767409601000,'{"time":{"created":1767409601000},"text":"bump the backoff cap"}');
insert into message values('m3','s3',1767409602000,'{"role":"user","time":{"created":1767409602000}}');
insert into part values('p3','m3','{"type":"text","text":"and log each retry","time":{"start":1767409602000}}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 3 {
		t.Fatalf("len=%d err=%v, want all three sessions", len(ss), err)
	}
	byID := map[string]model.Session{}
	for _, s := range ss {
		byID[s.ID] = s
	}
	for id, want := range map[string][]string{
		"s1": {"why does TestRetry flake"},
		"s2": {"what changed in the retry policy"},
		"s3": {"bump the backoff cap", "and log each retry"},
	} {
		var got []string
		for _, m := range byID[id].Messages {
			got = append(got, m.Text)
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%s messages = %q, want %q", id, got, want)
		}
	}
}

// The since read of a store in both layouts: a session touched only in message
// and part is read again with its session_message turns, or reading it again
// would replace the indexed session with half of it.
func TestOpencodeSinceReadsASplitSessionWhole(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "kilo.db")
	script := `create table session(id text primary key, directory text, title text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, time_updated integer, data text);
create table part(id text primary key, message_id text, session_id text, time_created integer, time_updated integer, data text);
create table session_message(id text primary key, session_id text, type text, seq integer, time_created integer, time_updated integer, data text);
insert into session values('s3','/w','split',1767409600000,1767409600000);
insert into session_message values('t3','s3','user',1,1767409601000,1767409601000,'{"time":{"created":1767409601000},"text":"bump the backoff cap"}');
insert into message values('m3','s3',1770000000000,1770000000000,'{"role":"user","time":{"created":1770000000000}}');
insert into part values('p3','m3','s3',1770000000000,1770000000000,'{"type":"text","text":"and log each retry","time":{"start":1770000000000}}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	// After the API turn, before the one sent with `kilo run -s`.
	ss, err := ParseOpencodeDBSince(db, time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC))
	if err != nil || len(ss) != 1 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	if m := ss[0].Messages; len(m) != 2 || m[0].Text != "bump the backoff cap" || m[1].Text != "and log each retry" {
		t.Fatalf("messages = %+v, want both turns in order", m)
	}
}

// A turn with no stamp keeps its place beside the turn read before it when a
// split session is put back in order; compared as-is it sorted to the top.
func TestSortTurnsByTimeKeepsUnstampedTurnsInPlace(t *testing.T) {
	at := func(s int) time.Time { return time.Unix(1767409600+int64(s), 0) }
	ms := []model.Message{
		{Text: "unstamped first"},
		{Text: "run 1", Time: at(2)},
		{Text: "run 1 output"},
		{Text: "run 2", Time: at(4)},
		{Text: "api 1", Time: at(1)},
		{Text: "api 2", Time: at(3)},
	}
	sortTurnsByTime(ms)
	got := make([]string, 0, len(ms))
	for _, m := range ms {
		got = append(got, m.Text)
	}
	want := "api 1|unstamped first|run 1|run 1 output|api 2|run 2"
	if strings.Join(got, "|") != want {
		t.Fatalf("order = %q, want %q", strings.Join(got, "|"), want)
	}
}
