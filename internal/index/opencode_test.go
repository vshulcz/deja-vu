package index

import (
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// opencode's database and its per-session diff file carry the same session id,
// so on the per-file path they arrive as two sessions with one identity. The
// messages merge either way — checked on a real store, all 60 sampled sessions
// kept both their conversation and their edits — but the pass reported the pair
// and a machine with 84 diffs was told that 84 of its sessions were clashing.
// One conversation in two stores is the goose case, and it is not reported
// either (#3791).
func TestADiffFileIsNotACollisionWithItsOwnSession(t *testing.T) {
	db := filepath.FromSlash("/data/opencode/opencode.db")
	diff := filepath.FromSlash("/data/opencode/storage/session_diff/ses_a.json")

	// The diff arriving second: the database row keeps the session.
	held := SessionMeta{Harness: "opencode", ID: "ses_a", Path: db, Project: "pool"}
	owns, collided := attributeSession(held, model.Session{Harness: "opencode", ID: "ses_a", Path: diff})
	if collided {
		t.Error("the diff beside the database was reported as a clashing transcript")
	}
	if owns {
		t.Error("the diff took the session's row from the database, which holds its project and title")
	}

	// And the other order, because a pass can read either first.
	heldDiff := SessionMeta{Harness: "opencode", ID: "ses_a", Path: diff}
	owns, collided = attributeSession(heldDiff, model.Session{Harness: "opencode", ID: "ses_a", Path: db, Project: "pool"})
	if collided {
		t.Error("the database row was reported as clashing with the diff it belongs to")
	}
	if !owns {
		t.Error("the database row did not take the session from the diff-only row")
	}

	// Two genuinely different transcripts under one id still are a collision:
	// that is what the report exists for.
	other := SessionMeta{Harness: "opencode", ID: "ses_a", Path: filepath.FromSlash("/data/opencode/other.db")}
	if _, collided = attributeSession(other, model.Session{Harness: "opencode", ID: "ses_a", Path: db}); !collided {
		t.Error("two databases under one id stopped being reported")
	}
}

func TestOpencodeDiffPathIsRecognisedByItsDirectory(t *testing.T) {
	for path, want := range map[string]bool{
		"/data/opencode/storage/session_diff/ses_a.json": true,
		"/elsewhere/session_diff/ses_b.json":             true,
		"/data/opencode/opencode.db":                     false,
		"/data/opencode/storage/session_diff":            false,
		"/data/opencode/storage/other/ses_a.json":        false,
		"/data/opencode/storage/session_diff/notes.txt":  false,
	} {
		if got := isOpencodeDiff(path); got != want {
			t.Errorf("isOpencodeDiff(%q) = %v, want %v", path, got, want)
		}
	}
}

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
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, stmts).CombinedOutput(); err != nil {
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

// The other side of the same rule: a session read again from the watermark must
// come back with its earlier turns, not the new one alone — the index replaces
// what it held for it (#2033, #4207).
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
	if out, sqlErr := exec.CommandContext(t.Context(), "sqlite3", db, stmts).CombinedOutput(); sqlErr != nil {
		t.Fatalf("sqlite3: %v %s", sqlErr, out)
	}
	var said strings.Builder
	if err = Ensure(dir, "", false, &said); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(said.String(), replacementPassMarker) {
		t.Fatalf("this was not the merge path, so it does not measure what it is about: %q", said.String())
	}
	s, ok, err := FindByIdentity(dir, "opencode", "s1")
	if err != nil || !ok {
		t.Fatalf("the session did not come back: %v %v", ok, err)
	}
	if len(s.Messages) != 2 {
		texts := make([]string, 0, len(s.Messages))
		for _, msg := range s.Messages {
			texts = append(texts, msg.Text)
		}
		t.Errorf("the session continued and came back with %d turns: %v", len(s.Messages), texts)
	}
	if hits, err := Search(dir, search.Options{Query: "pgbouncer", All: true}); err != nil || len(hits) == 0 {
		t.Errorf("the first turn is no longer searchable (%d hits, %v)", len(hits), err)
	}
}

// cursor keeps a database per workspace beside the global one, so "this pass
// read the store whole" is a fact about a store, not about the harness. A
// workspace seen for the first time has no watermark; a harness-wide flag let
// that pass replace sessions in the global store, which it had only read the
// tail of (#2033).
func TestANewCursorWorkspaceDoesNotTruncateTheGlobalStore(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	root := filepath.Join(tmp, "cursor")
	t.Setenv("DEJA_CURSOR_ROOT", root)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	global := filepath.Join(root, "globalStorage", "state.vscdb")
	if err := os.MkdirAll(filepath.Dir(global), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(db, sql string) {
		t.Helper()
		if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
			t.Fatalf("sqlite3: %v %s", err, out)
		}
	}
	run(global, `create table if not exists cursorDiskKV (key text primary key, value text);
insert or replace into cursorDiskKV values
 ('composerData:cu1', json('{"composerId":"cu1","name":"Chat","createdAt":1752600000000,"lastUpdatedAt":1752600100000,"fullConversationHeadersOnly":[{"bubbleId":"b1","type":1}]}')),
 ('bubbleId:cu1:b1', json('{"type":1,"text":"the first turn is about pgbouncer","timestamp":1752600001000,"workspaceProjectDir":"/w/app"}'));`)

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if hits, err := Search(dir, search.Options{Query: "pgbouncer", All: true}); err != nil || len(hits) == 0 {
		t.Fatalf("the first turn is not in the index (%d hits, %v), so this measures nothing", len(hits), err)
	}

	// One pass, two things: a workspace store deja has never seen, and a second
	// turn in the global conversation.
	ws := filepath.Join(root, "workspaceStorage", "w1", "state.vscdb")
	if err := os.MkdirAll(filepath.Dir(ws), 0o755); err != nil {
		t.Fatal(err)
	}
	run(ws, `create table if not exists cursorDiskKV (key text primary key, value text);
insert or replace into cursorDiskKV values
 ('composerData:cu2', json('{"composerId":"cu2","name":"Workspace chat","createdAt":1752700000000,"lastUpdatedAt":1752700100000,"fullConversationHeadersOnly":[{"bubbleId":"c1","type":1}]}')),
 ('bubbleId:cu2:c1', json('{"type":1,"text":"a workspace turn about caching","timestamp":1752700001000,"workspaceProjectDir":"/w/other"}'));`)
	run(global, `insert or replace into cursorDiskKV values
 ('composerData:cu1', json('{"composerId":"cu1","name":"Chat","createdAt":1752600000000,"lastUpdatedAt":1752600200000,"fullConversationHeadersOnly":[{"bubbleId":"b1","type":1},{"bubbleId":"b2","type":2}]}')),
 ('bubbleId:cu1:b2', json('{"type":2,"text":"the second turn is the answer","timestamp":1752600150000,"workspaceProjectDir":"/w/app"}'));`)
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(global, future, future); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	s, ok, err := FindByIdentity(dir, "cursor", "cu1")
	if err != nil || !ok {
		t.Fatalf("the global session did not come back: %v %v", ok, err)
	}
	if len(s.Messages) != 2 {
		texts := make([]string, 0, len(s.Messages))
		for _, msg := range s.Messages {
			texts = append(texts, msg.Text)
		}
		t.Errorf("the global conversation came back with %d turns after a new workspace appeared: %v", len(s.Messages), texts)
	}
	if hits, err := Search(dir, search.Options{Query: "pgbouncer", All: true}); err != nil || len(hits) == 0 {
		t.Errorf("the first turn is no longer searchable (%d hits, %v)", len(hits), err)
	}
	if hits, err := Search(dir, search.Options{Query: "caching", All: true}); err != nil || len(hits) == 0 {
		t.Errorf("the new workspace store was not indexed (%d hits, %v)", len(hits), err)
	}
}

// opencode names the project directory as the session's path, and a project can
// live inside another harness's root — a versioned ~/.claude is the obvious one.
// Asking the path what store a record came from answers with that harness there,
// which put opencode's records back among the per-file ones and brought #2033
// back with them.
func TestAnOpencodeProjectInsideAnotherHarnessRootStillCounts(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "opencode.db")
	t.Setenv("DEJA_OPENCODE_DB", db)
	project := filepath.Join(claudeRoot, "myconfig")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	seed := func(id, text string) {
		t.Helper()
		stmts := fmt.Sprintf(`
create table if not exists session (id text primary key, directory text, time_created integer, time_updated integer);
create table if not exists message (id text primary key, session_id text, data text, time_created integer);
create table if not exists part (id text primary key, message_id text, data text);
insert into session values ('%[1]s','%[3]s',0,0);
insert into message values ('m-%[1]s','%[1]s','{"role":"user"}',0);
insert into part values ('p-%[1]s','m-%[1]s',json_object('type','text','text','%[2]s'));
`, id, text, project)
		if out, err := exec.CommandContext(t.Context(), "sqlite3", db, stmts).CombinedOutput(); err != nil {
			t.Fatalf("sqlite3 seed: %v %s", err, out)
		}
	}
	seed("s1", "why does pgbouncer time out")

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	messages := func() int {
		t.Helper()
		s, ok, err := FindByIdentity(dir, "opencode", "s1")
		if err != nil || !ok {
			t.Fatalf("the session is not in the index: %v %v", ok, err)
		}
		return len(s.Messages)
	}
	if got := messages(); got != 1 {
		t.Fatalf("the build indexed %d messages for a one-turn session, so this measures nothing", got)
	}

	for i, id := range []string{"s2", "s3"} {
		seed(id, "an unrelated session")
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		if got := messages(); got != 1 {
			t.Fatalf("one turn on disk, %d in the index after pass %d", got, i+1)
		}
	}
}

// opencode creates the assistant message and its text part, part.time.start
// set, before the text streams in. A pass that reads the store in that moment
// stamps a watermark past both, and the text that lands a few milliseconds
// later is never asked for again: the reply was lost until a rebuild (#4207).
// Asking for it again must not hold the user turn twice either.
func TestAnOpencodeReplyWrittenDuringAPassReachesTheIndex(t *testing.T) {
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
	run := func(sql string) {
		t.Helper()
		if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
			t.Fatalf("sqlite3: %v %s", err, out)
		}
	}
	// The rows of ses_f0887bef0ffezIQpytYxwnxRzE as the half-written pass
	// saw them: the reply's part exists, started, and empty.
	run(`create table session(id text primary key, directory text, title text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, time_updated integer, data text);
create table part(id text primary key, message_id text, session_id text, time_created integer, time_updated integer, data text);
insert into session values('s1','/w/app','basalt',1790858248400,1790858251964);
insert into message values('m1','s1',1790858248489,1790858248489,'{"role":"user","time":{"created":1790858248489}}');
insert into part values('p1','m1','s1',1790858248489,1790858248489,'{"type":"text","text":"Reply ok. Topic: the basaltfinch rollout."}');
insert into message values('m2','s1',1790858248697,1790858251964,'{"role":"assistant","time":{"created":1790858248697}}');
insert into part values('p2','m2','s1',1790858251964,1790858251964,'{"type":"text","text":"","time":{"start":1790858251964}}');`)

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if m, err := readManifest(dir); err != nil || m.Files[db].LastUpdated == 0 {
		t.Fatalf("the store has no watermark, so the next pass reads it whole and measures nothing: %v", err)
	}

	// The text lands, opencode finishes the message and touches the session.
	run(`update part set data='{"type":"text","text":"ok, quillwort","time":{"start":1790858251964,"end":1790858251965}}', time_updated=1790858251965 where id='p2';
update message set time_updated=1790858252007 where id='m2';
update session set time_updated=1790858252010 where id='s1';`)
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(db, future, future); err != nil {
		t.Fatal(err)
	}
	var said strings.Builder
	if err := Ensure(dir, "", false, &said); err != nil {
		t.Fatal(err)
	}

	hits, err := Search(dir, search.Options{Query: "quillwort", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Errorf("the reply written during the first pass never reached the index (%q)", said.String())
	}
	s, ok, err := FindByIdentity(dir, "opencode", "s1")
	if err != nil || !ok {
		t.Fatalf("the session is not in the index: %v %v", ok, err)
	}
	texts := make([]string, 0, len(s.Messages))
	asked := 0
	for _, msg := range s.Messages {
		texts = append(texts, msg.Text)
		if strings.Contains(msg.Text, "basaltfinch") {
			asked++
		}
	}
	if asked != 1 || len(s.Messages) != 2 {
		t.Errorf("want the two turns once each, got %d: %q", len(s.Messages), texts)
	}
}

// A database file can grow with its earlier bytes in place, which is what the
// append path takes as a log that gained lines. A store read by whole sessions
// must not take it: the sessions it hands back are already in the index.
func TestAGrownWholeSessionStoreIsNotAppendedTo(t *testing.T) {
	tmp := t.TempDir()
	db := filepath.Join(tmp, "opencode.db")
	t.Setenv("DEJA_OPENCODE_DB", db)
	body := []byte(strings.Repeat("page\n", 200))
	if err := os.WriteFile(db, body, 0o600); err != nil {
		t.Fatal(err)
	}
	safe := lastCompleteLineOffset(db, int64(len(body)))
	old := FileState{Path: db, Size: int64(len(body)), SafeSize: safe, PrefixSample: filePrefixSample(db, safe), LastUpdated: 1}
	if err := os.WriteFile(db, append(body, []byte(strings.Repeat("more\n", 50))...), 0o600); err != nil {
		t.Fatal(err)
	}
	grown := FileState{Path: db, Size: int64(len(body) + 250)}
	if canAppendIncremental(map[string]FileState{db: grown}, map[string]FileState{db: old}) {
		t.Error("a grown opencode store went down the append path")
	}
}

// opencodeWithDiff is an opencode store holding ses_a's question, and the diff
// file opencode wrote beside it for that session.
func opencodeWithDiff(t *testing.T) (db, diff, dir string, run func(string)) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db = filepath.Join(tmp, "opencode", "opencode.db")
	t.Setenv("DEJA_OPENCODE_DB", db)
	diff = filepath.Join(tmp, "opencode", "storage", "session_diff", "ses_a.json")
	if err := os.MkdirAll(filepath.Dir(diff), 0o755); err != nil {
		t.Fatal(err)
	}
	run = func(sql string) {
		t.Helper()
		if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
			t.Fatalf("sqlite3: %v %s", err, out)
		}
	}
	run(`create table session(id text primary key, directory text, title text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, time_updated integer, data text);
create table part(id text primary key, message_id text, session_id text, time_created integer, time_updated integer, data text);
insert into session values('ses_a','/w/app','pool',1790858248400,1790858248489);
insert into message values('m1','ses_a',1790858248489,1790858248489,'{"role":"user"}');
insert into part values('p1','m1','ses_a',1790858248489,1790858248489,'{"type":"text","text":"why does the pool drop connections"}');`)
	writeOpencodeDiff(t, diff, "old line")
	dir = filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return db, diff, dir, run
}

func writeOpencodeDiff(t *testing.T, path, removed string) {
	t.Helper()
	body := `[{"file":"db/pool.go","status":"modified","additions":1,"deletions":1,"patch":"--- a/db/pool.go\n+++ b/db/pool.go\n@@ -1 +1 @@\n-` + removed + `\n+new line\n"}]`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Before every stamp in the database, so the file's time is not what a
	// pass is judged by.
	at := time.UnixMilli(1790858000000)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// rolesOf counts ses_a's records by role.
func rolesOf(t *testing.T, dir string) map[string]int {
	t.Helper()
	s, ok, err := FindByIdentity(dir, "opencode", "ses_a")
	if err != nil || !ok {
		t.Fatalf("ses_a is not in the index: %v %v", ok, err)
	}
	out := map[string]int{}
	for _, m := range s.Messages {
		out[m.Role]++
	}
	return out
}

func diffRecords(roles map[string]int) int {
	n := 0
	for role, c := range roles {
		if role != "user" && role != "assistant" {
			n += c
		}
	}
	return n
}

// A pass where only the database changed read ses_a again whole and dropped,
// by its key, the edits its diff file had given it — the diff had not changed,
// so nothing put them back (#4207).
func TestAnOpencodeReplyKeepsTheSessionsDiffRecords(t *testing.T) {
	db, _, dir, run := opencodeWithDiff(t)
	before := rolesOf(t, dir)
	if diffRecords(before) == 0 {
		t.Fatalf("the diff gave ses_a nothing, so this measures nothing: %v", before)
	}
	run(`insert into message values('m2','ses_a',1790858250000,1790858252007,'{"role":"assistant"}');
insert into part values('p2','m2','ses_a',1790858250000,1790858252000,'{"type":"text","text":"the pool is too small"}');
update session set time_updated=1790858252010 where id='ses_a';`)
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(db, future, future); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	after := rolesOf(t, dir)
	if after["assistant"] != 1 || after["user"] != 1 {
		t.Errorf("want the two turns once each: %v", after)
	}
	if diffRecords(after) != diffRecords(before) {
		t.Errorf("the session's diff records went from %v to %v", before, after)
	}
}

// The other half: a pass where only the diff file changed kept its old records,
// judged as the database's, and added the new ones beside them.
func TestAChangedOpencodeDiffIsNotIndexedTwice(t *testing.T) {
	_, diff, dir, _ := opencodeWithDiff(t)
	before := rolesOf(t, dir)
	writeOpencodeDiff(t, diff, "older zephyrine line")
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(diff, future, future); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	after := rolesOf(t, dir)
	if diffRecords(after) != diffRecords(before) || after["user"] != 1 {
		t.Errorf("the diff changed and its records went from %v to %v", before, after)
	}
	s, _, _ := FindByIdentity(dir, "opencode", "ses_a")
	read := false
	for _, m := range s.Messages {
		read = read || strings.Contains(m.Text, "zephyrine")
	}
	if !read {
		t.Error("the changed diff was not read")
	}
}

// A diff file's mtime is not a database stamp. Taken as one, it pushed the
// store's watermark past a row the database had stamped earlier and a pass had
// not yet seen, and that row was never asked for (#4207).
func TestAnOpencodeDiffTimeDoesNotMoveTheWatermark(t *testing.T) {
	db, _, dir, run := opencodeWithDiff(t)
	// A diff for a session the database does not hold, written two hours after
	// anything in it.
	late := filepath.Join(filepath.Dir(db), "storage", "session_diff", "ses_b.json")
	writeOpencodeDiff(t, late, "old line")
	at := time.UnixMilli(1790858248489 + 2*3600*1000)
	if err := os.Chtimes(late, at, at); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// A session stamped between the database's newest row and that mtime.
	run(`insert into session values('ses_c','/w/app','cache',1790858248489+3600000,1790858248489+3600000);
insert into message values('m3','ses_c',1790858248489+3600000,1790858248489+3600000,'{"role":"user"}');
insert into part values('p3','m3','ses_c',1790858248489+3600000,1790858248489+3600000,'{"type":"text","text":"warm the quillwort cache"}');`)
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(db, future, future); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if hits, err := Search(dir, search.Options{Query: "quillwort", All: true}); err != nil || len(hits) == 0 {
		t.Errorf("a row stamped before a diff file's mtime was never read (%d hits, %v)", len(hits), err)
	}
}

// Pinned, not wanted: a store read by whole sessions starts its counts over on
// each pass, so doctor's clipped count for opencode covers only what the last
// pass re-read until the next rebuild. Counting clips per session would keep
// it; until then this says what a user sees (#4207).
func TestOpencodeClipCountCoversOnlyTheLastPass(t *testing.T) {
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
	seedOpencodeSession(t, db, "s1", strings.Repeat("pgbouncer pool timed out and the retry took a second ", 1600), 1767322800000)
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
	// Two passes: the first still reads s1 again, the newest session sits
	// inside the few seconds every read goes back.
	for i, at := range []int64{1767326400000, 1767330000000} {
		seedOpencodeSession(t, db, fmt.Sprintf("s%d", i+2), "a short session", at)
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := clipped(); got != 0 {
		t.Errorf("the count after a pass that did not re-read s1 is %d; if it is right now, drop this pin", got)
	}
}

// A changed diff file was read as its session one sqlite3 run at a time, each
// with parents and titles beside it: ~430 ms a file on a 3.8 GB store, so a
// thousand diffs restored at once cost minutes. A session the database read
// already handed back with its diff folded in is not read again, and the rest
// are read together.
func TestChangedOpencodeDiffsAreReadInOneQuery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the sqlite3 wrapper is a shell script")
	}
	db, _, dir, run := opencodeWithDiff(t)
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	// Older than anything the since read goes back to.
	run(`insert into session values('ses_b','/w/app','b',1790850000000,1790850000100);
insert into message values('mb','ses_b',1790850000100,1790850000100,'{"role":"user"}');
insert into part values('pb','mb','ses_b',1790850000100,1790850000100,'{"type":"text","text":"why is b slow"}');
insert into session values('ses_c','/w/app','c',1790850000000,1790850000100);
insert into message values('mc','ses_c',1790850000100,1790850000100,'{"role":"user"}');
insert into part values('pc','mc','ses_c',1790850000100,1790850000100,'{"type":"text","text":"why is c slow"}');`)
	diffs := filepath.Join(filepath.Dir(db), "storage", "session_diff")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Every sqlite3 run of the next pass, logged and passed through.
	bin := t.TempDir()
	log := filepath.Join(bin, "runs.log")
	wrapper := "#!/bin/sh\nprintf '%s\\n' \"$*\" | tr '\\n' ' ' >> " + log + "\necho >> " + log + "\nexec " + sqlite + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "sqlite3"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// ses_a gets a reply, and all three diffs change in the same pass.
	run(`insert into message values('m2','ses_a',1790858250000,1790858252007,'{"role":"assistant"}');
insert into part values('p2','m2','ses_a',1790858250000,1790858252000,'{"type":"text","text":"the pool is too small"}');
update session set time_updated=1790858252010 where id='ses_a';`)
	future := time.Now().Add(time.Hour)
	for _, id := range []string{"ses_a", "ses_b", "ses_c"} {
		p := filepath.Join(diffs, id+".json")
		writeOpencodeDiff(t, p, "gone "+id+"marker line")
		if err := os.Chtimes(p, future, future); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(db, future, future); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(log)
	reads := 0
	for line := range strings.SplitSeq(string(b), "\n") {
		if strings.Contains(line, "join message m on m.session_id=s.id") {
			reads++
		}
	}
	// One for the database's own since read, one for ses_b and ses_c together.
	if reads != 2 {
		t.Errorf("the pass read sessions %d times, want 2", reads)
	}
	for _, id := range []string{"ses_a", "ses_b", "ses_c"} {
		s, ok, err := FindByIdentity(dir, "opencode", id)
		if err != nil || !ok {
			t.Fatalf("%s is not in the index: %v %v", id, ok, err)
		}
		read := false
		for _, m := range s.Messages {
			read = read || strings.Contains(m.Text, id+"marker")
		}
		if !read {
			t.Errorf("%s's changed diff was not read", id)
		}
	}
	if a := rolesOf(t, dir); a["user"] != 1 || a["assistant"] != 1 {
		t.Errorf("want ses_a's two turns once each: %v", a)
	}
}

// fromDatabase runs for every record the index holds on each pass that
// rewrites it. Telling an OpenCode-schema session from the harness's own files
// by asking the registry what its project directory was cost ~68 µs a record,
// against ~20 ns for any other harness: seconds per pass on a large opencode
// history (#4396).
func TestOpencodeSchemaRecordsAreJudgedWithoutTheRegistry(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	passStoreHarness.Range(func(k, _ any) bool { passStoreHarness.Delete(k); return true })
	for _, h := range []string{"opencode", "kilocode", "zcode"} {
		r := Record{Key: h + ":ses_1", SourcePath: filepath.Join(tmp, "coding", "proj")}
		if !fromDatabase(r) {
			t.Fatalf("%s: a session naming its project directory is not the store's", h)
		}
		// A registry walk is ~860 allocations; the path checks are a handful,
		// more on Windows, where filepath does more work. The bound tells the
		// two apart, not one platform's count from another's.
		if allocs := testing.AllocsPerRun(200, func() { fromDatabase(r) }); allocs > 100 {
			t.Errorf("%s: fromDatabase allocates %.0f times a record; it walks the registry", h, allocs)
		}
	}
}

// The harness's own files are not the database's: a Kilo extension task and a
// ZCode transcript are read per file and dropped when their file changes.
func TestOpencodeSchemaOwnFilesAreNotTheStore(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	for h, p := range map[string]string{
		"kilocode": filepath.Join(tmp, "tasks", "t1", "api_conversation_history.json"),
		"zcode":    filepath.Join(tmp, ".zcode", "sessions", "z1.jsonl"),
	} {
		if inOpencodeSchemaDB(h, p) {
			t.Errorf("%s: its own transcript counted as the database's", h)
		}
	}
}

// addOpencodeTurn adds one more message to a session already in an
// opencode-schema store.
func addOpencodeTurn(t *testing.T, db, session, id, text string, createdMillis int64) {
	t.Helper()
	stmts := fmt.Sprintf(`
insert into message values ('%[2]s','%[1]s','{"role":"assistant","time":{"created":%[4]d}}',%[4]d);
insert into part values ('p-%[2]s','%[2]s',json_object('type','text','text','%[3]s','time',json_object('start',%[4]d)));
update session set time_updated=%[4]d where id='%[1]s';
`, session, id, text, createdMillis)
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
}

// Kilo CLI and ZCode keep their sessions in OpenCode's schema, and the ingest
// knew opencode's database alone as a shared store. Any write to kilo.db sent
// every session in it back through the pass, which appended them to the
// records already held: a session doubled on each pass after a Kilo turn
// (#4396).
func TestOpencodeSchemaStoresKeepOneCopyOfEachSession(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	for _, c := range []struct{ harness, env string }{
		{"kilocode", "DEJA_KILO_DB"},
		{"zcode", "DEJA_ZCODE_DB"},
		{"opencode", "DEJA_OPENCODE_DB"},
	} {
		t.Run(c.harness, func(t *testing.T) {
			tmp := t.TempDir()
			setHome(t, tmp)
			t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
			t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
			t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
			t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
			for _, env := range []string{"DEJA_KILO_DB", "DEJA_ZCODE_DB", "DEJA_OPENCODE_DB"} {
				t.Setenv(env, filepath.Join(tmp, "none-"+env+".db"))
			}
			db := filepath.Join(tmp, c.harness+".db")
			t.Setenv(c.env, db)

			seedOpencodeSession(t, db, "s1", "fix the retry loop", 1767322800000)
			dir := filepath.Join(tmp, "index.db")
			if err := Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			count := func(id string) int {
				t.Helper()
				s, ok, err := FindByIdentity(dir, c.harness, id)
				if err != nil || !ok {
					t.Fatalf("%s:%s is not in the index (%v)", c.harness, id, err)
				}
				return len(s.Messages)
			}
			if got := count("s1"); got != 1 {
				t.Fatalf("the build holds %d messages for s1, so this measures nothing", got)
			}

			// The client writes the database without touching this session.
			later := time.Now().Add(time.Minute)
			if err := os.Chtimes(db, later, later); err != nil {
				t.Fatal(err)
			}
			var out strings.Builder
			if err := Ensure(dir, "", false, &out); err != nil {
				t.Fatal(err)
			}
			if got := count("s1"); got != 1 {
				t.Fatalf("after a pass over an untouched session it holds %d messages, want 1", got)
			}

			// A new session, and a turn on the old one: each held once, and
			// the old session keeps the turn it had.
			seedOpencodeSession(t, db, "s2", "a second session", 1767326400000)
			addOpencodeTurn(t, db, "s1", "m-s1-2", "the pool was too small", 1767330000000)
			if err := Ensure(dir, "", false, &out); err != nil {
				t.Fatal(err)
			}
			if got := count("s1"); got != 2 {
				t.Errorf("s1 holds %d messages after one more turn, want 2", got)
			}
			if got := count("s2"); got != 1 {
				t.Errorf("s2 holds %d messages, want 1", got)
			}
			if !strings.Contains(out.String(), replacementPassMarker) {
				t.Fatalf("this was not the merge path, so it does not measure what it is about: %q", out.String())
			}
		})
	}
}
