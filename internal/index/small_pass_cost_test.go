package index

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// smallPassStore writes n Claude Code sessions of four turns each, every turn
// a question, a command and its output, and builds an index over them. It
// returns the index and a function that rewrites session i shorter.
func smallPassStore(t *testing.T, n int) (string, func(i int)) {
	t.Helper()
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude"})
	loc := time.Local
	t.Cleanup(func() { time.Local = loc })
	time.Local = time.UTC
	proj := filepath.Join(tmp, "claude", "-tmp-proj")
	session := func(i, turns int) {
		id := fmt.Sprintf("s%02d", i)
		var body string
		var bodySb26 strings.Builder
		for turn := range turns {
			ts := fmt.Sprintf("%02d:%02d", turn, i)
			tid := fmt.Sprintf("t%d", turn)
			bodySb26.WriteString(parityClaudeLine(id, ts+"Z", "user", fmt.Sprintf(`"why does step %d of the migration hang"`, turn)) +
				parityClaudeLine(id, ts+"Z", "assistant", `[{"type":"tool_use","id":"`+tid+`","name":"Bash","input":{"command":"make migrate STEP=`+strconv.Itoa(turn)+`"}}]`) +
				parityClaudeLine(id, ts+"Z", "user", `[{"type":"tool_result","tool_use_id":"`+tid+`","content":"dial tcp 127.0.0.1:5432: connect: connection refused\nmake: *** [migrate] Error 1"}]`))
		}
		body += bodySb26.String()
		p := filepath.Join(proj, id+".jsonl")
		parityWrite(t, p, body, false)
		later := time.Now().Add(time.Duration(i+10*(4-turns)) * time.Second)
		_ = os.Chtimes(p, later, later)
	}
	for i := range n {
		session(i, 4)
	}
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	return inc, func(i int) { session(i, 3) }
}

// A pass that rewrites one session of many reads that session's records, not
// the whole log: the rest stay where they are, and the offset table says
// where. Walking the log was a second of a 20-session pass at 40,000.
func TestAReplacingPassReadsOnlyTheSessionsItChanges(t *testing.T) {
	const n = 12
	inc, shorten := smallPassStore(t, n)
	walked := storedRecordsWalked.Load()
	shorten(5)
	parityPass(t, inc, false)
	walked = storedRecordsWalked.Load() - walked
	if walked == 0 {
		t.Fatal("no replacing pass ran, so this measures nothing")
	}
	// The changed session's twelve records, out of n*12.
	if walked > 12 {
		t.Errorf("the pass walked %d stored records, want the changed session's 12", walked)
	}
	sameAsRebuild(t, inc)

	// The control: without the offset table the pass walks the log.
	if err := os.Remove(filepath.Join(inc, recordOffsetsFile)); err != nil {
		t.Fatal(err)
	}
	walked = storedRecordsWalked.Load()
	shorten(6)
	parityPass(t, inc, false)
	if walked = storedRecordsWalked.Load() - walked; walked < (n-1)*12 {
		t.Errorf("with no offset table the pass walked %d records, want them all, so the counter misses the walk", walked)
	}
	sameAsRebuild(t, inc)
}

// The command table and the session facts are mined again only from the
// sessions a pass changed, the first pass after a build included.
func TestAReplacingPassMinesOnlyTheChangedSessionsCommands(t *testing.T) {
	const n = 12
	inc, shorten := smallPassStore(t, n)
	mined := commandRecordsMined.Load()
	shorten(3)
	parityPass(t, inc, false)
	if mined = commandRecordsMined.Load() - mined; mined > 3 {
		t.Errorf("the command tables read %d command records, want the changed session's 3", mined)
	}
	if len(ReadCommands(inc)) == 0 || len(ReadSessionFacts(inc)) != n {
		t.Fatalf("commands %v, facts for %d sessions: this measures nothing", ReadCommands(inc), len(ReadSessionFacts(inc)))
	}
	sameAsRebuild(t, inc)

	// The control: without the state every command is read again.
	if err := os.Remove(commandsStatePath(inc)); err != nil {
		t.Fatal(err)
	}
	mined = commandRecordsMined.Load()
	shorten(4)
	parityPass(t, inc, false)
	if mined = commandRecordsMined.Load() - mined; mined < (n-2)*4 {
		t.Errorf("with no state the tables read %d command records, want them all, so the counter misses the walk", mined)
	}
	sameAsRebuild(t, inc)
}

// Resolving a schema store's database stats files, and a pass met every
// transcript's path for the first time: a second of stats on 40,000 of them.
func TestAReplacingPassResolvesTheSchemaStoresOnce(t *testing.T) {
	const n = 12
	inc, shorten := smallPassStore(t, n)
	var asked atomic.Int64
	saved := opencodeSchemaDBs
	t.Cleanup(func() { opencodeSchemaDBs = saved })
	counting := map[string]func() string{}
	for h, db := range saved {
		counting[h] = func() string { asked.Add(1); return db() }
	}
	opencodeSchemaDBs = counting
	// Walked whole, as a pass that cannot name its sessions walks it, so
	// every transcript's records are judged.
	if err := os.Remove(filepath.Join(inc, recordOffsetsFile)); err != nil {
		t.Fatal(err)
	}
	shorten(2)
	parityPass(t, inc, false)
	if got, most := asked.Load(), int64(4*len(saved)); got > most {
		t.Errorf("the pass resolved the schema databases %d times, want at most %d", got, most)
	}
}

// A word the dead records shared with live ones is known live from the start
// of its postings; reading each one whole made the scrub cost the size of the
// common words' postings.
func TestTheScrubReadsTheStartOfALiveWordsPostings(t *testing.T) {
	const n = 60
	inc, shorten := smallPassStore(t, n)
	// Dead records from earlier passes raise the size below which a word's
	// postings are read at all.
	for i := range n / 6 {
		shorten(i)
	}
	parityPass(t, inc, false)
	probed := postingBytesProbed.Load()
	shorten(n - 1)
	parityPass(t, inc, false)
	probed = postingBytesProbed.Load() - probed
	if probed > 4*liveProbeBytes {
		t.Errorf("the scrub read %d bytes of postings, want no more than the start of each word's", probed)
	}
	sameAsRebuild(t, inc)
}

// The facts are carried with the state they were written beside. A facts
// file that went missing is not read as "no session ran anything".
func TestCarriedCommandFactsMatchTheirState(t *testing.T) {
	inc, shorten := smallPassStore(t, 6)
	if err := os.Remove(sessionFactsPath(inc)); err != nil {
		t.Fatal(err)
	}
	shorten(1)
	parityPass(t, inc, false)
	if got := len(ReadSessionFacts(inc)); got != 6 {
		t.Errorf("facts for %d sessions after the pass, want 6", got)
	}
	sameAsRebuild(t, inc)
}

// A store that changed in the same pass carries the sessions it handed back,
// not the whole log: someone running opencode or grok changes its database on
// most passes (#5476).
func TestAPassThatAlsoChangesAStoreReadsOnlyTheSessionsItChanged(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}
	const n = 12
	inc, shorten := smallPassStore(t, n)
	tmp := filepath.Dir(inc)
	oc, grok := filepath.Join(tmp, "opencode.db"), filepath.Join(tmp, "grok.db")
	t.Setenv("DEJA_OPENCODE_DB", oc)
	t.Setenv("DEJA_GROK_DB", grok)
	clock := time.Now().Add(-2 * time.Hour).UnixMilli()
	// Turn i of session n at minute 10i+n: each one past the slack a since
	// read goes back, and a later turn past every earlier one.
	ocMsg := func(n, i int) string {
		at := clock + int64(10*i+n)*60_000
		return fmt.Sprintf(`insert into message values('oc%[1]d-m%[2]d','oc%[1]d',%[3]d,%[3]d,'{"role":"user","time":{"created":%[3]d}}');
insert into part values('oc%[1]d-p%[2]d','oc%[1]d-m%[2]d','oc%[1]d',%[3]d,%[3]d,'{"type":"text","text":"why does the pool %[1]d step %[2]d hang"}');
update session set time_updated=%[3]d where id='oc%[1]d';
`, n, i, at)
	}
	grokMsg := func(n, i int) string {
		return fmt.Sprintf(`INSERT INTO messages VALUES ('g%[1]d',%[2]d,'user','{"content":"why does the queue %[1]d step %[2]d stall"}','2026-07-27T10:%02[3]d:00.000Z');
`, n, i, 10*i+n)
	}
	ocSQL := `create table session(id text primary key, directory text, title text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, time_updated integer, data text);
create table part(id text primary key, message_id text, session_id text, time_created integer, time_updated integer, data text);
`
	grokSQL := `CREATE TABLE workspaces (id TEXT PRIMARY KEY, canonical_path TEXT);
CREATE TABLE sessions (id TEXT PRIMARY KEY, workspace_id TEXT, title TEXT, cwd_last TEXT, created_at TEXT);
CREATE TABLE messages (session_id TEXT, seq INTEGER, role TEXT, message_json TEXT, created_at TEXT);
`
	var grokSQLSb204 strings.Builder
	var ocSQLSb204 strings.Builder
	for i := range 6 {
		ocSQLSb204.WriteString(fmt.Sprintf("insert into session values('oc%d','/w/app','pool %d',%d,%d);\n", i, i, clock, clock) + ocMsg(i, 0) + ocMsg(i, 1))
		grokSQLSb204.WriteString(fmt.Sprintf("INSERT INTO sessions VALUES ('g%d','w','queue %d','/work/api','2026-07-27T10:00:00.000Z');\n", i, i) + grokMsg(i, 0) + grokMsg(i, 1))
	}
	grokSQL += grokSQLSb204.String()
	ocSQL += ocSQLSb204.String()
	paritySQL(t, oc, ocSQL)
	paritySQL(t, grok, grokSQL)
	touch := func(p string, s int) {
		at := time.Now().Add(time.Duration(s) * time.Minute)
		_ = os.Chtimes(p, at, at)
	}
	parityPass(t, inc, false)
	if !HasSession(inc, "oc3") || !HasSession(inc, "g3") {
		t.Fatal("the stores were not indexed, so this measures nothing")
	}

	walked := storedRecordsWalked.Load()
	shorten(5)
	paritySQL(t, oc, ocMsg(3, 2))
	paritySQL(t, grok, grokMsg(3, 2))
	touch(oc, 1)
	touch(grok, 1)
	parityPass(t, inc, false)
	walked = storedRecordsWalked.Load() - walked
	// The transcript's twelve records, and in each store the changed
	// session and the one at the watermark, which a since read takes again.
	if walked == 0 || walked > 12+2*(2+2) {
		t.Errorf("the pass walked %d stored records, want at most the changed sessions' 20 of %d", walked, n*12+24)
	}
	sameAsRebuild(t, inc)

	// A store change alone reads the session it changed.
	walked = storedRecordsWalked.Load()
	paritySQL(t, oc, ocMsg(1, 3))
	touch(oc, 2)
	parityPass(t, inc, false)
	if walked = storedRecordsWalked.Load() - walked; walked == 0 || walked > 2+3 {
		t.Errorf("an opencode message walked %d stored records, want at most the changed session's and the watermark's 5", walked)
	}
	sameAsRebuild(t, inc)
}
