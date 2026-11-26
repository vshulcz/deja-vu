package sources

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func execSQLite(t *testing.T, db, sql string) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 %s: %v\n%s", filepath.Base(db), err, out)
	}
}

func explainPlan(t *testing.T, db, query string) string {
	t.Helper()
	out, err := sqliteOutput(db, "EXPLAIN QUERY PLAN "+query)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN failed: %v\nquery: %s", err, query)
	}
	return string(out)
}

func sessionByID(ss []model.Session) map[string]model.Session {
	out := make(map[string]model.Session, len(ss))
	for _, s := range ss {
		out[s.ID] = s
	}
	return out
}

func assertSessionsEqual(t *testing.T, got, want model.Session) {
	t.Helper()
	if got.Harness != want.Harness || got.ID != want.ID || got.Project != want.Project ||
		got.Path != want.Path || got.Title != want.Title || got.Parent != want.Parent || got.Kind != want.Kind {
		t.Fatalf("session metadata mismatch:\n got: %+v\nwant: %+v", got, want)
	}
	if !got.Started.Equal(want.Started) || !got.Updated.Equal(want.Updated) {
		t.Fatalf("session time mismatch for %s: got (%v, %v), want (%v, %v)",
			want.ID, got.Started, got.Updated, want.Started, want.Updated)
	}
	if len(got.Messages) != len(want.Messages) {
		t.Fatalf("session %s message count = %d, want %d\n got: %+v\nwant: %+v",
			want.ID, len(got.Messages), len(want.Messages), got.Messages, want.Messages)
	}
	for i := range want.Messages {
		if got.Messages[i].Role != want.Messages[i].Role ||
			got.Messages[i].Text != want.Messages[i].Text ||
			!got.Messages[i].Time.Equal(want.Messages[i].Time) {
			t.Fatalf("session %s msg[%d] mismatch:\n got: %+v\nwant: %+v",
				want.ID, i, got.Messages[i], want.Messages[i])
		}
	}
}

func TestSQLiteCursorAnchorsAndFallback(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "anchors.db")
	execSQLite(t, db, `
create table items (id text primary key, val text);
insert into items values ('k1', 'v1'), ('k2', 'v2');
`)
	specs := []sqliteTableSpec{{Name: "items", KeyExpr: "cast(id as text)"}}

	cur1 := captureSQLiteTableCursor(db, specs)
	if cur1 == "" {
		t.Fatal("Capture empty cursor")
	}

	// Appending a tail row preserves anchors and allows delta.
	execSQLite(t, db, `insert into items values ('k3', 'v3');`)
	prev, cur2, canDelta := prepareSQLiteDelta(db, cur1, specs)
	if !canDelta || prev.Tables["items"].MaxRowID != 2 || cur2 == "" || cur2 == cur1 {
		t.Fatalf("after INSERT: canDelta=%v prev=%+v cur2=%q", canDelta, prev, cur2)
	}

	// INSERT OR REPLACE on a historical key assigns a new rowid at the tail,
	// leaving first_rowid=1 unchanged or moving max_rowid forward cleanly.
	execSQLite(t, db, `insert or replace into items values ('k2', 'v2-new');`)
	prev, cur3, canDelta := prepareSQLiteDelta(db, cur2, specs)
	if !canDelta || prev.Tables["items"].MaxRowID != 3 || cur3 == "" {
		t.Fatalf("after tail INSERT OR REPLACE: canDelta=%v prev=%+v cur3=%q", canDelta, prev, cur3)
	}

	// Deleting rows including the tail (max_rowid) shrinks max_rowid -> anchor invalidated.
	execSQLite(t, db, `delete from items where id='k2';`)
	_, curShrank, canDelta := prepareSQLiteDelta(db, cur3, specs)
	if canDelta {
		t.Fatalf("after tail DELETE expected canDelta=false, got true")
	}
	if curShrank == "" {
		t.Fatal("expected refreshed cursor after tail DELETE")
	}

	// Inserting a new row after the previous tail was deleted can reuse prev MaxRowID (4)
	// with a different key ('k4' != 'k2') -> keyAtPrevMax check against cur3 detects the shift.
	execSQLite(t, db, `insert into items values ('k4', 'v4');`)
	_, curReused, canDelta := prepareSQLiteDelta(db, cur3, specs)
	if canDelta {
		t.Fatalf("after tail DELETE+INSERT reusing max_rowid expected canDelta=false, got true")
	}
	if curReused == "" {
		t.Fatal("expected refreshed cursor after reuse")
	}

	// Fresh append after refreshed cursor succeeds.
	execSQLite(t, db, `insert into items values ('k5', 'v5');`)
	_, _, canDelta = prepareSQLiteDelta(db, curReused, specs)
	if !canDelta {
		t.Fatal("expected canDelta=true after refreshed cursor")
	}

	// Corrupt JSON cursor & mismatched version fall back to canDelta=false while capturing fresh cursor.
	for _, bad := range []string{"", "{bad json", `{"v":99,"tables":{"items":{"max_rowid":1}}}`} {
		_, nextTok, ok := prepareSQLiteDelta(db, bad, specs)
		if ok || nextTok == "" {
			t.Fatalf("bad cursor %q: canDelta=%v nextTok=%q", bad, ok, nextTok)
		}
	}

	// WITHOUT ROWID table cannot provide rowid anchors; capture returns "" and delta falls back safely.
	withoutDB := filepath.Join(dir, "norowid.db")
	execSQLite(t, withoutDB, `
create table items (id text primary key, val text) without rowid;
insert into items values ('a', '1');
`)
	if got := captureSQLiteTableCursor(withoutDB, specs); got != "" {
		t.Fatalf("WITHOUT ROWID capture = %q, want empty", got)
	}
	_, nextNoRow, canDeltaNoRow := prepareSQLiteDelta(withoutDB, cur1, specs)
	if canDeltaNoRow || nextNoRow != "" {
		t.Fatalf("WITHOUT ROWID prepareSQLiteDelta = (%v, %q), want (false, empty)", canDeltaNoRow, nextNoRow)
	}
}

func TestSQLiteDeltaQueryPlansUseRowIDSeek(t *testing.T) {
	dir := t.TempDir()

	// Cursor cursorDiskKV with unary +key keeps SQLite on INTEGER PRIMARY KEY (rowid>?).
	cursorDB := filepath.Join(dir, "state.vscdb")
	execSQLite(t, cursorDB, `
create table cursorDiskKV (key text primary key, value text);
create table ItemTable (key text primary key, value text);
`)
	plan := explainPlan(t, cursorDB,
		`select json_object('key',cast(key as text)) from cursorDiskKV where rowid > 10 and +key >= 'bubbleId:' and +key < 'bubbleId;' and value is not null`)
	if !strings.Contains(plan, "INTEGER PRIMARY KEY (rowid>?)") {
		t.Fatalf("cursorDiskKV plan did not use INTEGER PRIMARY KEY (rowid>?):\n%s", plan)
	}

	// OpenCode v1 & v2 tables.
	ocV1 := filepath.Join(dir, "oc_v1.db")
	execSQLite(t, ocV1, `
create table session (id text primary key, directory text, time_created integer, time_updated integer);
create table message (id text primary key, session_id text, time_created integer, time_updated integer, data text);
create table part (id text primary key, message_id text, session_id text, time_created integer, time_updated integer, data text);
`)
	for _, q := range []string{
		`select id from session where rowid > 10`,
		`select session_id from message where rowid > 10`,
		`select session_id from part where rowid > 10`,
	} {
		p := explainPlan(t, ocV1, q)
		if !strings.Contains(p, "INTEGER PRIMARY KEY (rowid>?)") {
			t.Fatalf("OpenCode v1 plan for %q did not use rowid seek:\n%s", q, p)
		}
	}

	ocV2 := filepath.Join(dir, "oc_v2.db")
	execSQLite(t, ocV2, `
create table session_v2 (id text primary key, directory text, time_created integer, time_updated integer);
create table session_message (id text primary key, session_id text, type text, seq integer, time_created integer, time_updated integer, data text);
`)
	for _, q := range []string{
		`select id from session_v2 where rowid > 10`,
		`select session_id from session_message where rowid > 10`,
	} {
		p := explainPlan(t, ocV2, q)
		if !strings.Contains(p, "INTEGER PRIMARY KEY (rowid>?)") {
			t.Fatalf("OpenCode v2 plan for %q did not use rowid seek:\n%s", q, p)
		}
	}

	// Goose, Hermes, OpenClaw rowid seeks.
	gooseDB := filepath.Join(dir, "goose.db")
	execSQLite(t, gooseDB, `
create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text, session_type text);
create table messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
`)
	if p := explainPlan(t, gooseDB, `select session_id from messages where rowid > 10`); !strings.Contains(p, "INTEGER PRIMARY KEY (rowid>?)") {
		t.Fatalf("Goose messages plan did not use rowid seek:\n%s", p)
	}

	hermesDB := filepath.Join(dir, "hermes.db")
	execSQLite(t, hermesDB, `create table messages (id integer primary key autoincrement, session_id text, role text, content text, timestamp real);`)
	if p := explainPlan(t, hermesDB, `select session_id from messages where rowid > 10`); !strings.Contains(p, "INTEGER PRIMARY KEY (rowid>?)") {
		t.Fatalf("Hermes messages plan did not use rowid seek:\n%s", p)
	}

	openclawDB := filepath.Join(dir, "openclaw.sqlite")
	execSQLite(t, openclawDB, `create table transcript_events (session_id text not null, seq integer not null, event_json text, created_at integer not null, primary key (session_id, seq));`)
	if p := explainPlan(t, openclawDB, `select session_id from transcript_events where rowid > 10`); !strings.Contains(p, "INTEGER PRIMARY KEY (rowid>?)") {
		t.Fatalf("OpenClaw transcript_events plan did not use rowid seek:\n%s", p)
	}
}

func TestParseCursorDBDeltaBudgetRenameAndVacuum(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.vscdb")
	var b strings.Builder
	b.WriteString("create table cursorDiskKV (key text primary key, value text);\n")
	b.WriteString("create table ItemTable (key text primary key, value text);\nbegin;\n")
	baseMS := int64(1780000000000)
	for i := range 25 {
		cid := fmt.Sprintf("comp-%02d", i)
		ts := baseMS + int64(i)*60000
		fmt.Fprintf(&b, `insert into cursorDiskKV values('composerData:%s', '{"composerId":"%s","name":"Composer %02d","createdAt":%d,"lastUpdatedAt":%d}');`+"\n",
			cid, cid, i, ts, ts+5000)
		for j := range 10 {
			role := 1
			if j%2 == 1 {
				role = 2
			}
			fmt.Fprintf(&b, `insert into cursorDiskKV values('bubbleId:%s:b%02d', '{"type":%d,"text":"msg %d-%d","timestamp":%d,"workspaceProjectDir":"/Users/alex/work/p%d"}');`+"\n",
				cid, j, role, i, j, ts+int64(j)*500, i)
		}
	}
	b.WriteString("insert into ItemTable values('ui.state', 'initial');\ncommit;\n")
	execSQLite(t, db, b.String())

	wm := time.UnixMilli(baseMS + 24*60000 + 5000)
	cur := CaptureCursorDBCursor(db)
	if cur == "" {
		t.Fatal("CaptureCursorDBCursor empty")
	}

	// 1. ItemTable-only write -> 0 decoded domain rows, empty sessions, valid cursor.
	execSQLite(t, db, `insert or replace into ItemTable values('ui.state', 'updated-panel');`)
	ResetSQLiteDecodedRows()
	ss, cur, err := ParseCursorDBDelta(db, wm, cur)
	if err != nil || len(ss) != 0 {
		t.Fatalf("ItemTable write: len=%d err=%v", len(ss), err)
	}
	if n := SQLiteDecodedRows(); n != 0 {
		t.Fatalf("ItemTable write decoded %d rows, want 0", n)
	}

	// 2. Append one bubble to comp-03 (without bumping composerData.lastUpdatedAt) -> only comp-03 returned whole.
	bTS := baseMS + 3*60000 + 6000 // older than wm! proves rowid catch works even when timestamp lags wm
	execSQLite(t, db, fmt.Sprintf(`insert into cursorDiskKV values('bubbleId:comp-03:b10', '{"type":2,"text":"delta answer","timestamp":%d,"workspaceProjectDir":"/Users/alex/work/p3"}');`, bTS))
	ResetSQLiteDecodedRows()
	ss, cur, err = ParseCursorDBDelta(db, wm, cur)
	if err != nil || len(ss) != 1 || ss[0].ID != "comp-03" {
		t.Fatalf("append 1 bubble: got %+v err=%v", ss, err)
	}
	if len(ss[0].Messages) != 11 {
		t.Fatalf("comp-03 messages = %d, want 11 whole", len(ss[0].Messages))
	}
	// 1 delta bubble key + 1 composer row + 11 bubble rows = 13 rows decoded (vs 275 in full store).
	if n := SQLiteDecodedRows(); n > 20 {
		t.Fatalf("delta decoded %d rows, expected O(1) session rows (<= 20)", n)
	}

	// 3. In-place composer rename without bumping lastUpdatedAt (INSERT OR REPLACE bumps rowid).
	execSQLite(t, db, fmt.Sprintf(`insert or replace into cursorDiskKV values('composerData:comp-05', '{"composerId":"comp-05","name":"Renamed Five","createdAt":%d,"lastUpdatedAt":%d}');`,
		baseMS+5*60000, baseMS+5*60000+5000))
	ResetSQLiteDecodedRows()
	ss, cur, err = ParseCursorDBDelta(db, wm, cur)
	if err != nil || len(ss) != 1 || ss[0].ID != "comp-05" || ss[0].Title != "Renamed Five" {
		t.Fatalf("INSERT OR REPLACE rename: got %+v err=%v", ss, err)
	}
	if len(ss[0].Messages) != 10 {
		t.Fatalf("renamed comp-05 messages = %d, want 10 whole", len(ss[0].Messages))
	}

	// 4. In-place composer rename bumping lastUpdatedAt (UPDATE preserves rowid).
	newWM := wm.Add(10 * time.Second)
	execSQLite(t, db, fmt.Sprintf(`update cursorDiskKV set value = '{"composerId":"comp-07","name":"Updated Seven","createdAt":%d,"lastUpdatedAt":%d}' where key='composerData:comp-07';`,
		baseMS+7*60000, newWM.UnixMilli()))
	ss, cur, err = ParseCursorDBDelta(db, wm, cur)
	if err != nil || len(ss) != 1 || ss[0].ID != "comp-07" || ss[0].Title != "Updated Seven" {
		t.Fatalf("UPDATE rename: got %+v err=%v", ss, err)
	}
	wm = newWM

	// 5. DELETE + VACUUM + new bubble falls back and matches ParseCursorDB for the new composer.
	execSQLite(t, db, fmt.Sprintf(`
delete from cursorDiskKV where key like 'bubbleId:comp-01:%%' or key='composerData:comp-01';
vacuum;
insert into cursorDiskKV values('composerData:comp-25', '{"composerId":"comp-25","name":"Post Vacuum","createdAt":%d,"lastUpdatedAt":%d}');
insert into cursorDiskKV values('bubbleId:comp-25:b0', '{"type":1,"text":"after vacuum","timestamp":%d,"workspaceProjectDir":"/Users/alex/work/p25"}');
`, newWM.Add(time.Minute).UnixMilli(), newWM.Add(2*time.Minute).UnixMilli(), newWM.Add(time.Minute).UnixMilli()))
	ss, cur, err = ParseCursorDBDelta(db, wm, cur)
	if err != nil || cur == "" {
		t.Fatalf("post-vacuum delta err=%v cur=%q", err, cur)
	}
	gotByID := sessionByID(ss)
	if _, ok := gotByID["comp-25"]; !ok {
		t.Fatalf("post-vacuum missing comp-25: %v", gotByID)
	}
	all, err := ParseCursorDB(db)
	if err != nil {
		t.Fatal(err)
	}
	wantByID := sessionByID(all)
	assertSessionsEqual(t, gotByID["comp-25"], wantByID["comp-25"])
}

func TestParseOpencodeV1AndDerivedDelta(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "opencode.db")
	var b strings.Builder
	b.WriteString(`create table session(id text primary key, project_id text, parent_id text, directory text, title text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, time_updated integer, data text);
create table part(id text primary key, message_id text, session_id text, time_created integer, time_updated integer, data text);
begin;
`)
	base := int64(1780000000000)
	for i := range 25 {
		sid := fmt.Sprintf("ses_%02d", i)
		ts := base + int64(i)*60000
		fmt.Fprintf(&b, `insert into session values('%s','p1',null,'/Users/alex/work/app%d','Title %02d',%d,%d);`+"\n",
			sid, i, i, ts, ts+2000)
		for m := range 4 {
			mid := fmt.Sprintf("msg_%02d_%d", i, m)
			role := "user"
			if m%2 == 1 {
				role = "assistant"
			}
			fmt.Fprintf(&b, `insert into message values('%s','%s',%d,%d,'{"role":"%s"}');`+"\n",
				mid, sid, ts+int64(m)*400, ts+int64(m)*400, role)
			fmt.Fprintf(&b, `insert into part values('prt_%02d_%d','%s','%s',%d,%d,'{"type":"text","text":"turn %d-%d"}');`+"\n",
				i, m, mid, sid, ts+int64(m)*400, ts+int64(m)*400, i, m)
		}
	}
	b.WriteString("commit;\n")
	execSQLite(t, db, b.String())

	// Past the 5s opencodeSinceSlack window so baseline sessions stay outside since slack.
	wm := time.UnixMilli(base + 24*60000 + 2000).Add(10 * time.Second)
	cur := CaptureOpencodeDBCursor(db)
	if cur == "" {
		t.Fatal("CaptureOpencodeDBCursor empty")
	}

	// 1. Append 1 turn to ses_04 -> only ses_04 returned whole; row budget proportional.
	tTurn := wm.Add(30 * time.Second).UnixMilli()
	execSQLite(t, db, fmt.Sprintf(`
insert into message values('msg_04_4','ses_04',%d,%d,'{"role":"user"}');
insert into part values('prt_04_4','msg_04_4','ses_04',%d,%d,'{"type":"text","text":"delta question"}');
update session set time_updated = %d where id='ses_04';
`, tTurn, tTurn, tTurn, tTurn, tTurn))
	ResetSQLiteDecodedRows()
	ss, cur, err := ParseOpencodeDBDelta(db, wm, cur)
	if err != nil || len(ss) != 1 || ss[0].ID != "ses_04" {
		t.Fatalf("append 1 turn: got %+v err=%v", ss, err)
	}
	if len(ss[0].Messages) != 5 {
		t.Fatalf("ses_04 messages = %d, want 5 whole", len(ss[0].Messages))
	}
	if n := SQLiteDecodedRows(); n > 20 {
		t.Fatalf("opencode delta decoded %d rows, expected <= 20", n)
	}

	// 2. #4207 in-place streaming part update (part.time_updated advances in place).
	wm = time.UnixMilli(tTurn).Add(10 * time.Second)
	tStream := wm.Add(30 * time.Second).UnixMilli()
	execSQLite(t, db, fmt.Sprintf(`
update part set time_updated = %d, data = '{"type":"text","text":"streamed final reply"}' where id='prt_04_3';
`, tStream))
	ss, cur, err = ParseOpencodeDBDelta(db, wm, cur)
	if err != nil || len(ss) != 1 || ss[0].ID != "ses_04" {
		t.Fatalf("#4207 streaming update: got %+v err=%v", ss, err)
	}
	if !strings.Contains(ss[0].Messages[3].Text, "streamed final reply") {
		t.Fatalf("missing updated streaming part text: %+v", ss[0].Messages)
	}

	// 3. #4450 in-place session.title rename.
	wm = time.UnixMilli(tStream).Add(10 * time.Second)
	tRename := wm.Add(30 * time.Second).UnixMilli()
	execSQLite(t, db, fmt.Sprintf(`update session set title = 'Renamed Eleven', time_updated = %d where id='ses_11';`, tRename))
	ss, cur, err = ParseOpencodeDBDelta(db, wm, cur)
	if err != nil || len(ss) != 1 || ss[0].ID != "ses_11" || ss[0].Title != "Renamed Eleven" {
		t.Fatalf("#4450 rename: got %+v err=%v", ss, err)
	}
	if len(ss[0].Messages) != 4 {
		t.Fatalf("renamed ses_11 messages = %d, want 4 whole", len(ss[0].Messages))
	}

	// 4. Appended subagent session whose directory is empty inherits un-moved parent's project.
	wm = time.UnixMilli(tRename).Add(10 * time.Second)
	tChild := wm.Add(30 * time.Second).UnixMilli()
	execSQLite(t, db, fmt.Sprintf(`
insert into session values('ses_child','p1','ses_02','','Subagent Task',%d,%d);
insert into message values('msg_c_0','ses_child',%d,%d,'{"role":"user"}');
insert into part values('prt_c_0','msg_c_0','ses_child',%d,%d,'{"type":"text","text":"child task"}');
`, tChild, tChild, tChild, tChild, tChild, tChild))
	ss, cur, err = ParseOpencodeDBDelta(db, wm, cur)
	if err != nil || len(ss) != 1 || ss[0].ID != "ses_child" {
		t.Fatalf("subagent append: got %+v err=%v", ss, err)
	}
	all, err := ParseOpencodeDB(db)
	if err != nil {
		t.Fatal(err)
	}
	assertSessionsEqual(t, ss[0], sessionByID(all)["ses_child"])
	if ss[0].Path != "/Users/alex/work/app2" {
		t.Fatalf("ses_child Path = %q, want inherited /Users/alex/work/app2", ss[0].Path)
	}

	// 5. Same delta pipeline serves Kilo, ZCode, MiMo, and TeleAgent (TeleAgent preserves Path == db).
	for _, tc := range []struct {
		name    string
		parse   func(string, time.Time, string) ([]model.Session, string, error)
		harness string
		path    string
	}{
		{"kilo", ParseKiloDBDelta, "kilocode", "/Users/alex/work/app2"},
		{"zcode", ParseZCodeDBDelta, "zcode", "/Users/alex/work/app2"},
		{"mimo", ParseMimoDBDelta, "mimo", "/Users/alex/work/app2"},
		{"teleagent", ParseTeleagentDBDelta, "teleagent", db},
	} {
		got, _, derr := tc.parse(db, time.UnixMilli(tRename).Add(10*time.Second), CaptureOpencodeDBCursor(db))
		if derr != nil || len(got) < 1 {
			t.Fatalf("%s delta: len=%d err=%v", tc.name, len(got), derr)
		}
		m := sessionByID(got)
		if s, ok := m["ses_child"]; !ok || s.Harness != tc.harness {
			t.Fatalf("%s missing/bad ses_child: %+v", tc.name, m["ses_child"])
		} else if s.Path != tc.path {
			t.Fatalf("%s ses_child Path = %q, want %q", tc.name, s.Path, tc.path)
		}
	}

	// 6. Top-level session with empty directory falls back to Path == db on both delta and full parse.
	wm = time.UnixMilli(tChild).Add(10 * time.Second)
	tNoDir := wm.Add(30 * time.Second).UnixMilli()
	execSQLite(t, db, fmt.Sprintf(`
insert into session values('ses_nodir','p1',null,'','No Dir Session',%d,%d);
insert into message values('msg_nd_0','ses_nodir',%d,%d,'{"role":"user"}');
insert into part values('prt_nd_0','msg_nd_0','ses_nodir',%d,%d,'{"type":"text","text":"standalone nodir"}');
`, tNoDir, tNoDir, tNoDir, tNoDir, tNoDir, tNoDir))
	ss, cur, err = ParseOpencodeDBDelta(db, wm, cur)
	if err != nil || len(ss) != 1 || ss[0].ID != "ses_nodir" || ss[0].Path != db {
		t.Fatalf("top-level nodir delta: got %+v err=%v want Path=%q", ss, err, db)
	}
	allAfterNoDir, err := ParseOpencodeDB(db)
	if err != nil {
		t.Fatal(err)
	}
	assertSessionsEqual(t, ss[0], sessionByID(allAfterNoDir)["ses_nodir"])

	// Ensure cur stays usable on empty follow-up pass.
	wm = time.UnixMilli(tNoDir).Add(10 * time.Second)
	none, cur2, err := ParseOpencodeDBDelta(db, wm, cur)
	if err != nil || len(none) != 0 || cur2 == "" {
		t.Fatalf("no-op opencode delta: len=%d cur2=%q err=%v", len(none), cur2, err)
	}
}

func TestParseOpencodeV1UnstampedAndV2Delta(t *testing.T) {
	// Unstamped v1 store: message/part lack time_updated and part lacks session_id;
	// #4207 updates session.time_updated when streaming finishes.
	v1DB := filepath.Join(t.TempDir(), "opencode_v1_unstamped.db")
	execSQLite(t, v1DB, `
create table session(id text primary key, project_id text, parent_id text, directory text, title text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, data text);
create table part(id text primary key, message_id text, data text);
insert into session values('s1','p1',null,'/Users/alex/work/api','API',1780000000000,1780000005000);
insert into message values('m1','s1',1780000001000,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"q1"}');
insert into message values('m2','s1',1780000002000,'{"role":"assistant"}');
insert into part values('p2','m2','{"type":"text","text":"partial"}');
insert into session values('s2','p1',null,'/Users/alex/work/web','Web',1780000010000,1780000012000);
insert into message values('m3','s2',1780000011000,'{"role":"user"}');
insert into part values('p3','m3','{"type":"text","text":"q2"}');
`)
	wm := time.UnixMilli(1780000012000).Add(10 * time.Second)
	cur := CaptureOpencodeDBCursor(v1DB)
	tEdit := wm.Add(30 * time.Second).UnixMilli()
	execSQLite(t, v1DB, fmt.Sprintf(`
update part set data = '{"type":"text","text":"complete unstamped reply"}' where id='p2';
update session set time_updated = %d where id='s1';
`, tEdit))
	ss, _, err := ParseOpencodeDBDelta(v1DB, wm, cur)
	if err != nil || len(ss) != 1 || ss[0].ID != "s1" {
		t.Fatalf("unstamped v1 streaming: got %+v err=%v", ss, err)
	}
	if !strings.Contains(ss[0].Messages[1].Text, "complete unstamped reply") {
		t.Fatalf("unstamped v1 missing reply text: %+v", ss[0].Messages)
	}

	// OpenCode v2 store: session_v2 + session_message + streaming in-place update + 1-turn append.
	v2DB, _ := opencodeV2Many(t, 20)
	allBefore, err := ParseOpencodeDB(v2DB)
	if err != nil || len(allBefore) == 0 {
		t.Fatal(err)
	}
	var maxU time.Time
	for _, s := range allBefore {
		if s.Updated.After(maxU) {
			maxU = s.Updated
		}
	}
	maxU = maxU.Add(10 * time.Second)
	curV2 := CaptureOpencodeDBCursor(v2DB)
	tV2 := maxU.Add(30 * time.Second).UnixMilli()
	execSQLite(t, v2DB, fmt.Sprintf(`
update session_message set time_updated = %d, data = '{"time":{"created":%d},"content":[{"type":"text","text":"v2 streamed body"}]}' where id='ses_0002-2';
`, tV2, tV2-60000))
	ResetSQLiteDecodedRows()
	ssV2, curV2, err := ParseOpencodeDBDelta(v2DB, maxU, curV2)
	if err != nil || len(ssV2) != 1 || ssV2[0].ID != "ses_0002" {
		t.Fatalf("v2 in-place update: got %+v err=%v", ssV2, err)
	}
	if !strings.Contains(ssV2[0].Messages[1].Text, "v2 streamed body") {
		t.Fatalf("v2 updated body missing: %+v", ssV2[0].Messages)
	}
	if n := SQLiteDecodedRows(); n > 15 {
		t.Fatalf("v2 delta decoded %d rows, want <= 15", n)
	}

	// Follow up with VACUUM + append on v2.
	wmV2Next := time.UnixMilli(tV2).Add(10 * time.Second)
	tV2Next := wmV2Next.Add(30 * time.Second).UnixMilli()
	execSQLite(t, v2DB, fmt.Sprintf(`
delete from session_message where session_id='ses_0001';
delete from session_v2 where id='ses_0001';
vacuum;
insert into session_message values('ses_0003-4','ses_0003','user',4,%d,%d,'{"time":{"created":%d},"text":"post-vacuum turn"}');
`, tV2Next, tV2Next, tV2Next))
	ssVac, _, err := ParseOpencodeDBDelta(v2DB, wmV2Next, curV2)
	if err != nil || len(ssVac) != 1 || ssVac[0].ID != "ses_0003" {
		t.Fatalf("v2 post-vacuum delta: got %+v err=%v", ssVac, err)
	}
	allAfter, err := ParseOpencodeDB(v2DB)
	if err != nil {
		t.Fatal(err)
	}
	assertSessionsEqual(t, ssVac[0], sessionByID(allAfter)["ses_0003"])
}

func TestParseGooseHermesOpenClawDeltaParity(t *testing.T) {
	// 1. Goose: multi-session append + in-place description rename.
	gooseDB := filepath.Join(t.TempDir(), "sessions.db")
	var gb strings.Builder
	gb.WriteString(`CREATE TABLE sessions (id TEXT PRIMARY KEY, name TEXT, description TEXT, working_dir TEXT, created_at TEXT, updated_at TEXT, session_type TEXT);
CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT, role TEXT, content_json TEXT, created_timestamp INTEGER);
`)
	for i := range 15 {
		fmt.Fprintf(&gb, `INSERT INTO sessions VALUES ('g%02d','n%02d','desc %02d','/Users/alex/work/g%d','2026-01-01T00:00:00Z','2026-01-01T00:00:10Z','user');`+"\n", i, i, i, i)
		fmt.Fprintf(&gb, `INSERT INTO messages (session_id,role,content_json,created_timestamp) VALUES ('g%02d','user','[{"type":"text","text":"goose init %02d"}]',1780000000);`+"\n", i, i)
	}
	execSQLite(t, gooseDB, gb.String())
	wmGoose := time.Unix(1780000010, 0)
	curGoose := CaptureGooseDBCursor(gooseDB)
	execSQLite(t, gooseDB, fmt.Sprintf(`
INSERT INTO messages (session_id,role,content_json,created_timestamp) VALUES ('g04','assistant','[{"type":"text","text":"goose delta reply"}]',1780000100);
UPDATE sessions SET description='Goose Renamed 07', updated_at='%s' WHERE id='g07';
`, wmGoose.Add(2*time.Minute).UTC().Format("2006-01-02 15:04:05")))
	ResetSQLiteDecodedRows()
	gDelta, curGoose2, err := ParseGooseDBDelta(gooseDB, wmGoose, curGoose)
	if err != nil || len(gDelta) != 2 || curGoose2 == "" {
		t.Fatalf("goose delta: len=%d cur=%q err=%v", len(gDelta), curGoose2, err)
	}
	if n := SQLiteDecodedRows(); n > 10 {
		t.Fatalf("goose delta decoded %d rows, want <= 10", n)
	}
	gAll, err := ParseGooseDB(gooseDB)
	if err != nil {
		t.Fatal(err)
	}
	wantG := sessionByID(gAll)
	for _, s := range gDelta {
		assertSessionsEqual(t, s, wantG[s.ID])
	}

	// 2. Hermes: multi-session append + 2-hop ancestor cwd inheritance.
	hermesDir := filepath.Join(t.TempDir(), "profile")
	var hb strings.Builder
	hb.WriteString(`CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT, parent_session_id TEXT, cwd TEXT, model_config TEXT);` + "\n")
	for i := range 15 {
		fmt.Fprintf(&hb, `INSERT INTO sessions VALUES ('h%02d','cli',NULL,'/Users/alex/work/h%d','{}');`+"\n", i, i)
		fmt.Fprintf(&hb, `INSERT INTO messages (session_id,role,content,timestamp) VALUES ('h%02d','user','hermes turn %02d',1785000000.0);`+"\n", i, i)
	}
	// Parent h02 has grandparent h01 -> h00 (with cwd '/Users/alex/work/h0').
	hb.WriteString(`INSERT INTO sessions VALUES ('h-parent','cli','h00','','{}');` + "\n")
	hb.WriteString(`INSERT INTO messages (session_id,role,content,timestamp) VALUES ('h-parent','user','parent msg',1785000000.0);` + "\n")
	hermesDB := writeHermesDB(t, hermesDir, hb.String())
	wmHermes := time.Unix(1785000010, 0)
	curHermes := CaptureHermesDBCursor(hermesDB)

	execSQLite(t, hermesDB, `
INSERT INTO sessions VALUES ('h-grandchild','cli','h-parent','','{}');
INSERT INTO messages (session_id,role,content,timestamp) VALUES ('h-grandchild','user','grandchild task',1785000100.0);
`)
	ResetSQLiteDecodedRows()
	hDelta, curHermes2, err := ParseHermesDBDelta(hermesDB, wmHermes, curHermes)
	if err != nil || len(hDelta) != 1 || hDelta[0].ID != "h-grandchild" || curHermes2 == "" {
		t.Fatalf("hermes delta: got %+v cur=%q err=%v", hDelta, curHermes2, err)
	}
	if n := SQLiteDecodedRows(); n > 10 {
		t.Fatalf("hermes delta decoded %d rows, want <= 10", n)
	}
	hAll, err := ParseHermesDB(hermesDB)
	if err != nil {
		t.Fatal(err)
	}
	assertSessionsEqual(t, hDelta[0], sessionByID(hAll)["h-grandchild"])

	// 3. OpenClaw: composite (session_id, seq) key delta + child spawn lineage preserved when parent untouched.
	openclawDB := filepath.Join(t.TempDir(), "main", "agent", "openclaw-agent.sqlite")
	var ob strings.Builder
	ob.WriteString(`create table session_windows (session_id text not null, session_key text not null, created_at integer not null, spawned_by text, parent_session_key text, primary key (session_id));
create table transcript_events (session_id text not null, seq integer not null, event_json text, created_at integer not null, primary key (session_id, seq));
`)
	baseOC := int64(1786000000000)
	for i := range 12 {
		sid := fmt.Sprintf("oc-%02d", i)
		skey := fmt.Sprintf("agent:main:w%02d", i)
		fmt.Fprintf(&ob, `insert into session_windows values('%s','%s',%d,null,null);`+"\n", sid, skey, baseOC)
		fmt.Fprintf(&ob, `insert into transcript_events values('%s',0,'{"type":"session","version":3,"id":"%s","timestamp":"2026-08-10T09:00:00Z","cwd":"/Users/alex/work/oc"}',%d);`+"\n", sid, sid, baseOC+1000)
		fmt.Fprintf(&ob, `insert into transcript_events values('%s',1,'{"type":"message","id":"u1","timestamp":"2026-08-10T09:00:01Z","message":{"role":"user","content":"init %02d"}}',%d);`+"\n", sid, i, baseOC+2000)
	}
	execSQLite(t, openclawDB, ob.String())
	wmOC := time.UnixMilli(baseOC + 5000)
	curOC := CaptureOpenClawDBCursor(openclawDB)
	execSQLite(t, openclawDB, fmt.Sprintf(`
insert into session_windows values('oc-child','agent:main:subagent:1',%d,'agent:main:w02','agent:main:w02');
insert into transcript_events values('oc-child',0,'{"type":"session","version":3,"id":"oc-child","timestamp":"2026-08-10T09:05:00Z","cwd":"/Users/alex/work/oc"}',%d);
insert into transcript_events values('oc-child',1,'{"type":"message","id":"u2","timestamp":"2026-08-10T09:05:01Z","message":{"role":"user","content":"subagent delta"}}',%d);
`, baseOC+10000, baseOC+10000, baseOC+11000))
	ResetSQLiteDecodedRows()
	ocDelta, curOC2, err := ParseOpenClawDBDelta(openclawDB, wmOC, curOC)
	if err != nil || len(ocDelta) != 1 || ocDelta[0].ID != "oc-child" || curOC2 == "" {
		t.Fatalf("openclaw delta: got %+v cur=%q err=%v", ocDelta, curOC2, err)
	}
	if n := SQLiteDecodedRows(); n > 10 {
		t.Fatalf("openclaw delta decoded %d rows, want <= 10", n)
	}
	ocAll, err := ParseOpenClawDB(openclawDB)
	if err != nil {
		t.Fatal(err)
	}
	assertSessionsEqual(t, ocDelta[0], sessionByID(ocAll)["oc-child"])
}
