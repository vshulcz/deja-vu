package index

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
)

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
		if out, err := exec.Command("sqlite3", db, sql).CombinedOutput(); err != nil {
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
	var texts []string
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
		if out, err := exec.Command("sqlite3", db, sql).CombinedOutput(); err != nil {
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
	real, err := exec.LookPath("sqlite3")
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
	wrapper := "#!/bin/sh\nprintf '%s\\n' \"$*\" | tr '\\n' ' ' >> " + log + "\necho >> " + log + "\nexec " + real + " \"$@\"\n"
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
	for _, line := range strings.Split(string(b), "\n") {
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
