package index

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
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
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
}

// A database-backed store reads only what is new — the since cursor the index
// stamps as LastUpdated — but it changes like any other file, so the merge
// branch treated it as re-read whole and started its ingest counts over. The
// database growing by one message threw away what the rest of it holds (#2025).
//
// grok, because its reader selects messages. opencode was the case here until
// it began handing back touched sessions whole (#4207); a store that does that
// starts its counts over, the goose trade below.
func TestADatabaseThatGrowsKeepsItsIngestCounts(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none-opencode.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "grok.db")
	t.Setenv("DEJA_GROK_DB", db)
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(tmp, "grok"))

	long := strings.Repeat("pgbouncer pool timed out and the retry took a second ", 1600)
	if len(long) < maxIndexedText {
		t.Fatalf("the fixture message is %d bytes, under the %d that gets it clipped", len(long), maxIndexedText)
	}
	seedGrokDB(t, db, [][2]string{{long, "2026-01-01T10:00:00.000Z"}})

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
		return m.IngestHealth["grok"].ClippedMessages
	}
	if got := clipped(); got != 1 {
		t.Fatalf("the build clipped %d messages, so this measures nothing", got)
	}

	// The store grows by a message that has nothing wrong with it. The pass
	// reads only that message, so it cannot speak for the rest of the store.
	seedGrokDB(t, db, [][2]string{{"a short second message", "2026-01-01T11:00:00.000Z"}})
	var out strings.Builder
	if err := Ensure(dir, "", false, &out); err != nil {
		t.Fatal(err)
	}
	if said := out.String(); !strings.Contains(said, replacementPassMarker) {
		t.Fatalf("this was not the merge path, so it does not measure what it is about: %q", said)
	}
	if hits, err := Search(dir, search.Options{Query: "short second message", All: true}); err != nil || len(hits) == 0 {
		t.Fatalf("the new message was not read (%d hits, %v), so the store did not grow the way this expects", len(hits), err)
	}
	if got := clipped(); got != 1 {
		t.Errorf("the clipped message is still in the database and the count is %d", got)
	}
}

// seedGooseTurn adds one message to a goose store, creating the tables if the
// file is new and moving the session's updated_at with it.
func seedGooseTurn(t *testing.T, db, session, role, text string, created int64) { //nolint:unparam // the call names the session it extends
	t.Helper()
	stmts := fmt.Sprintf(`
create table if not exists sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table if not exists messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert or ignore into sessions values ('%[1]s','g','a goose session','/tmp/app', datetime(%[4]d,'unixepoch'), datetime(%[4]d,'unixepoch'));
update sessions set updated_at = datetime(%[4]d,'unixepoch') where id='%[1]s';
insert into messages (session_id,role,content_json,created_timestamp) values ('%[1]s','%[2]s',json_array(json_object('type','text','text','%[3]s')),%[4]d);
`, session, role, text, created)
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
}

// goose asks for everything in a session that was touched, not only the new
// messages, so a continued session hands back turns already counted. With
// nothing resetting a database's counts (#2025) those turns were counted again
// on every pass — a store in daily use would report clips in the thousands.
//
// The timestamps are sqlite's own format, which is what a real store holds; the
// since clause puts both sides through datetime(), so the format no longer
// decides the comparison (#2030).
func TestAGooseSessionThatContinuesIsNotCountedTwice(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "goose.db")
	t.Setenv("DEJA_GOOSE_DB", db)

	long := strings.Repeat("pgbouncer pool timed out and the retry took a second ", 1600)
	seedGooseTurn(t, db, "g1", "user", long, 1767322800)
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
		// By the store, which is the key doctor files these under since #2234
		// — "goose-db" is the file kind.
		return m.IngestHealth["goose"].ClippedMessages
	}
	if got := clipped(); got != 1 {
		t.Fatalf("the build clipped %d messages, so this measures nothing", got)
	}

	// The session carries on. Each pass hands the long message back again.
	for i, ts := range []int64{1767326400, 1767330000} {
		seedGooseTurn(t, db, "g1", "assistant", "a short answer", ts)
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		if got := clipped(); got != 1 {
			t.Fatalf("one long message in the store, pass %d says %d clipped", i+1, got)
		}
	}
}

// The other side of handing back only the new turns: what the index already
// holds has to survive it. A partial return that replaced the session would
// drop every turn it did not include (#2032).
func TestAGooseSessionKeepsItsEarlierTurns(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "goose.db")
	t.Setenv("DEJA_GOOSE_DB", db)

	seedGooseTurn(t, db, "g1", "user", "why does pgbouncer time out", 1785166187)
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if hits, err := Search(dir, search.Options{Query: "pgbouncer", All: true}); err != nil || len(hits) == 0 {
		t.Fatalf("the first turn is not in the index (%d hits, %v), so this measures nothing", len(hits), err)
	}

	seedGooseTurn(t, db, "g1", "assistant", "the pool was too small", 1785166787)
	var out strings.Builder
	if err := Ensure(dir, "", false, &out); err != nil {
		t.Fatal(err)
	}
	if said := out.String(); !strings.Contains(said, replacementPassMarker) {
		t.Fatalf("this was not the merge path, so it does not measure what it is about: %q", said)
	}
	s, ok, err := FindByIdentity(dir, "goose", "g1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || len(s.Messages) != 2 {
		t.Fatalf("the session came back with %d messages (found=%v): the new turn replaced what was there", len(s.Messages), ok)
	}
	if hits, err := Search(dir, search.Options{Query: "pgbouncer", All: true}); err != nil || len(hits) == 0 {
		t.Errorf("the first turn is gone from the index after the session continued (%d hits, %v)", len(hits), err)
	}
}

// runStoreSQL feeds a script to the sqlite3 CLI on stdin: a fixture that opens
// with a `--` comment would otherwise be read as an option.
func runStoreSQL(t *testing.T, db, sql string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v: %s", err, out)
	}
}

// stampedStoreEnv is hermeticIndexEnv plus the three stores it does not pin,
// so a contributor with real grok, goose or hermes history runs the same test
// as CI and each case sees only the store it wrote.
func stampedStoreEnv(t *testing.T) string {
	t.Helper()
	tmp := hermeticIndexEnv(t)
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_GROK_DB", filepath.Join(tmp, "none-grok.db"))
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(tmp, "none-grok"))
	t.Setenv("DEJA_ZED_DB", filepath.Join(tmp, "none-zed.db"))
	t.Setenv("DEJA_HERMES_HOME", filepath.Join(tmp, "none-hermes"))
	t.Setenv("DEJA_HERMES_DB", "")
	return tmp
}

// zedJSONThreadRow writes one thread the way Zed does, minus the compression:
// the hex body is read straight through when data_type is "json", so a fixture
// needs no zstd on the machine running it.
func zedJSONThreadRow(id, updated string, texts ...string) string {
	msgs := make([]string, 0, len(texts))
	for i, text := range texts {
		msgs = append(msgs, `{"User":{"id":"u`+string(rune('0'+i))+`","content":[{"Text":"`+text+`"}]}}`)
	}
	body := `{"version":"0.3.0","title":"` + id + `","updated_at":"` + updated + `",` +
		`"initial_project_snapshot":{"worktree_snapshots":[{"worktree_path":"/work/api"}]},` +
		`"messages":[` + strings.Join(msgs, ",") + `]}`
	return "insert or replace into threads (id,summary,updated_at,data_type,data) values ('" +
		id + "','" + id + "','" + updated + "','json',x'" + hex.EncodeToString([]byte(body)) + "');\n"
}

// Every store deja reads through SQLite is stamped with a watermark and parsed
// from it (#2075), so the second pass over one of them has three things to get
// right at once: the session that arrived is indexed, the sessions that did not
// move are still there, and a session that gained a turn holds the turns it had
// as well as the new one — once each.
//
// The three stamped late (grok, zed, hermes) are the cases here. The turn below
// the watermark is what a partial parse cannot hand back, and the record count
// is what says whether the pass replaced the session or added to it.
func TestEveryStampedStoreSurvivesTheSecondPass(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	cases := []struct {
		harness string
		// seed writes the store and returns its path: two sessions, the older
		// of which nothing touches again, and an opening turn in the newer.
		seed func(t *testing.T, tmp string) string
		// grow adds a session and a turn to the one that already existed.
		grow func(t *testing.T, db string)
		// key is the session that gains a turn, and turns the count it should
		// hold afterwards.
		key   string
		turns int
	}{{
		harness: "grok",
		seed: func(t *testing.T, tmp string) string {
			t.Helper()
			db := filepath.Join(tmp, "grok.db")
			t.Setenv("DEJA_GROK_DB", db)
			runStoreSQL(t, db, `CREATE TABLE workspaces (id TEXT PRIMARY KEY, canonical_path TEXT);
CREATE TABLE sessions (id TEXT PRIMARY KEY, workspace_id TEXT, title TEXT, cwd_last TEXT, created_at TEXT);
CREATE TABLE messages (session_id TEXT, seq INTEGER, role TEXT, message_json TEXT, created_at TEXT);
INSERT INTO sessions VALUES ('gq','w','Quiet one','/work/api','2026-07-26T10:00:00.000Z');
INSERT INTO messages VALUES ('gq',0,'user','{"content":"needlequiet a session nobody touches again"}','2026-07-26T10:00:00.000Z');
INSERT INTO sessions VALUES ('gl','w','Pool timeouts','/work/api','2026-07-27T10:00:00.000Z');
INSERT INTO messages VALUES ('gl',0,'user','{"content":"needleopening the opening question"}','2026-07-27T10:00:00.000Z');
INSERT INTO messages VALUES ('gl',1,'assistant','{"content":[{"type":"text","text":"needlemiddle the first answer"}]}','2026-07-27T11:00:00.000Z');`)
			return db
		},
		grow: func(t *testing.T, db string) {
			t.Helper()
			runStoreSQL(t, db, `INSERT INTO messages VALUES ('gl',2,'user','{"content":"needlelatest the follow up"}','2026-07-27T13:00:00.000Z');
INSERT INTO sessions VALUES ('gn','w','Brand new','/work/api','2026-07-27T14:00:00.000Z');
INSERT INTO messages VALUES ('gn',0,'user','{"content":"needlearrival a session that did not exist"}','2026-07-27T14:00:00.000Z');`)
		},
		key: "grok:gl", turns: 3,
	}, {
		harness: "zed",
		seed: func(t *testing.T, tmp string) string {
			t.Helper()
			db := filepath.Join(tmp, "threads.db")
			t.Setenv("DEJA_ZED_DB", db)
			runStoreSQL(t, db, `create table threads (id text primary key, summary text not null, updated_at text not null, data_type text not null, data blob not null, parent_id text, folder_paths text, folder_paths_order text, created_at text);
`+zedJSONThreadRow("zq", "2026-07-26T10:00:00+00:00", "needlequiet a session nobody touches again")+
				zedJSONThreadRow("zl", "2026-07-27T11:00:00+00:00", "needleopening the opening question", "needlemiddle the first answer"))
			return db
		},
		grow: func(t *testing.T, db string) {
			t.Helper()
			runStoreSQL(t, db, zedJSONThreadRow("zl", "2026-07-27T13:00:00+00:00",
				"needleopening the opening question", "needlemiddle the first answer", "needlelatest the follow up")+
				zedJSONThreadRow("zn", "2026-07-27T14:00:00+00:00", "needlearrival a session that did not exist"))
		},
		key: "zed:zl", turns: 3,
	}, {
		harness: "hermes",
		seed: func(t *testing.T, tmp string) string {
			t.Helper()
			// Hermes is found by walking its home, not by a store path, so the
			// fixture has to live where HermesDBs looks — a DEJA_HERMES_DB the
			// registry does not match leaves the store unparsed and the test
			// measuring nothing.
			home := filepath.Join(tmp, "hermes")
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("DEJA_HERMES_HOME", home)
			db := filepath.Join(home, "state.db")
			runStoreSQL(t, db, `CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, role TEXT NOT NULL, content TEXT, tool_call_id TEXT, tool_calls TEXT, tool_name TEXT, timestamp REAL NOT NULL, token_count INTEGER, finish_reason TEXT);
INSERT INTO messages (session_id,role,content,timestamp) VALUES
 ('hq','user','needlequiet a session nobody touches again',1785000000.0),
 ('hl','user','needleopening the opening question',1785003600.0),
 ('hl','assistant','needlemiddle the first answer',1785007200.5);`)
			return db
		},
		grow: func(t *testing.T, db string) {
			t.Helper()
			runStoreSQL(t, db, `INSERT INTO messages (session_id,role,content,timestamp) VALUES
 ('hl','user','needlelatest the follow up',1785014400.0),
 ('hn','user','needlearrival a session that did not exist',1785018000.0);`)
		},
		key: "hermes:hl", turns: 3,
	}}

	for _, c := range cases {
		t.Run(c.harness, func(t *testing.T) {
			tmp := stampedStoreEnv(t)
			db := c.seed(t, tmp)

			dir := filepath.Join(tmp, "index.db")
			if err := Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			hits := func(needle string) int {
				t.Helper()
				ss, err := Search(dir, search.Options{Query: needle, All: true})
				if err != nil {
					t.Fatal(err)
				}
				return len(ss)
			}
			// The premise: without this the assertions below hold for a store
			// deja never read.
			for _, needle := range []string{"needlequiet", "needleopening", "needlemiddle"} {
				if n := hits(needle); n != 1 {
					t.Fatalf("%s: %d hits before the second pass, so the store was not indexed at all", needle, n)
				}
			}
			m, err := readManifest(dir)
			if err != nil {
				t.Fatal(err)
			}
			if m.Files[db].LastUpdated == 0 {
				t.Fatalf("%s was not stamped, so its since-the-watermark parser never runs", db)
			}

			c.grow(t, db)
			if err = Ensure(dir, "", false, nil); err != nil {
				t.Fatal(err)
			}
			for _, needle := range []string{
				"needlearrival", // the session that arrived
				"needlequiet",   // the session nothing touched
				"needleopening", // the turn below the watermark
				"needlemiddle",  // the turn at the watermark
				"needlelatest",  // the turn that arrived
			} {
				if n := hits(needle); n != 1 {
					t.Errorf("%s: %d hits after the second pass, want 1", needle, n)
				}
			}
			// Once each: a store parsed from its watermark that hands a session
			// back whole has to replace what the index holds for it, and one
			// that hands back the new turns alone has to add to it. Either way
			// the session ends with the turns the store has.
			m, err = readManifest(dir)
			if err != nil {
				t.Fatal(err)
			}
			rs, err := recordsForKey(filepath.Join(dir, "records.bin"), tablesFromManifest(m), c.key)
			if err != nil {
				t.Fatal(err)
			}
			if len(rs) != c.turns {
				t.Errorf("%s holds %d records, want %d", c.key, len(rs), c.turns)
			}
		})
	}
}

// A question asked in plain words carries filler alongside the one word that
// identifies the answer. "How many bikes do I own?" ranks on many, bikes, own —
// and sessions holding the two filler words used to count as real matches while
// the single session that says bikes rode behind all of them, because the split
// between a real match and a weak one counted terms and never asked what they
// were worth.
//
// On the 1910-session day0 corpus that put the answer at rank 46 of 50, under
// wineries and invasive species. It is at 10 now.
func TestOneDecisiveWordOutranksTwoFillerOnes(t *testing.T) {
	// The filler words have to be common for this to be the real situation:
	// in a corpus of four everything is rare, idf collapses, and an earlier
	// tier answers before relevance is ever asked.
	texts := make([]string, 0, 41)
	for range 40 {
		texts = append(texts, "there are many ways to own one of these and many people own several")
	}
	answer := len(texts)
	texts = append(texts, "I keep three bikes in the garage and ride the blue to work")
	dir := nlIndex(t, texts...)

	// Straight at the relevance tier rather than through the ladder: on a corpus
	// this small an earlier tier answers first, and the ordering under test is
	// this one's.
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	result, err := relevanceSearch(dir, m, query.Options{Query: "How many bikes do I own?", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) == 0 {
		t.Fatal("a question whose rare word is in the corpus returned nothing")
	}
	want := string(rune('a' + answer)) //nolint:gosec // answer is a small index
	if got := result.Sessions[0].ID; got != want {
		rank := -1
		for i, s := range result.Sessions {
			if s.ID == want {
				rank = i + 1
			}
		}
		t.Errorf("the session holding the rare word ranked %d of %d, want it first (got %q first)",
			rank, len(result.Sessions), got)
	}
}

// The other half of the same rule, and the reason it is gated. Where the words
// a rare term stands alone against are not words at all, one surviving anchor
// is noise however rare it is, and the tier owes the query silence rather than
// its best guess.
func TestALoneRareWordAmongTyposStillSaysNothing(t *testing.T) {
	dir := nlIndex(t, "I keep three bikes in the garage and ride the blue to work")
	result, err := SearchDetailed(dir, search.Options{Query: "zzqx wwvv bikes", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 0 {
		t.Errorf("a query of two typos and one real word answered anyway: %d sessions", len(result.Sessions))
	}
}

// The same gemini chat can exist as both session-x.json and session-x.jsonl.
// Full rebuilds and — the harder case — incremental updates that touch both
// files in one pass must not write the shared messages twice. Distinct
// messages under one session key (codex history style) must still merge.
func TestDuplicateFormatSessionsDoNotDuplicateMessages(t *testing.T) {
	tmp := t.TempDir()
	gem := filepath.Join(tmp, "gemini")
	chats := filepath.Join(gem, "tmp", "s", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON := func() {
		doc := `{"sessionId":"dup1","startTime":"2026-07-15T10:00:00.000Z","lastUpdated":"2026-07-15T10:00:00.000Z","messages":[{"id":"m1","timestamp":"2026-07-15T10:00:01.000Z","type":"user","content":"dupneedle question"}]}`
		if err := os.WriteFile(filepath.Join(chats, "session-dup.json"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON()
	jsonl := filepath.Join(chats, "session-dup.jsonl")
	head := `{"sessionId":"dup1","startTime":"2026-07-15T10:00:00.000Z","lastUpdated":"2026-07-15T10:05:00.000Z"}` + "\n" +
		`{"id":"m1","timestamp":"2026-07-15T10:00:01.000Z","type":"user","content":"dupneedle question"}` + "\n"
	if err := os.WriteFile(jsonl, []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_GEMINI_ROOT", gem)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "nc"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "nx"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "n.db"))
	dir := filepath.Join(tmp, "index.db")
	o := search.Options{Query: "dupneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	count := func(what string) int {
		ss, err := Search(dir, search.Options{Query: what, All: true})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, s := range ss {
			n += len(s.Messages)
		}
		return n
	}
	if got := count("dupneedle question"); got != 1 {
		t.Fatalf("after rebuild: %d copies, want 1", got)
	}

	// Touch both format files in one incremental pass.
	f, err := os.OpenFile(jsonl, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"id":"m2","timestamp":"2026-07-15T10:06:00.000Z","type":"user","content":"dupneedle again"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	writeJSON() // rewrite -> mtime change on the twin
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	if got := count("dupneedle question"); got != 1 {
		t.Fatalf("after dual-file incremental: %d copies, want 1", got)
	}
	if got := count("dupneedle again"); got != 1 {
		t.Fatalf("new message: %d copies, want 1", got)
	}
}

// A dsh session that arrives as session.v3.jsonl after the first build has to
// reach the index through the incremental path, and so does every turn it gains
// after that. The path sees the file either way; what decides it is whether the
// deepseek kind matches the name. When it did not, the pass reported
// "changed_files=1 ... sessions=0" and the session stayed unsearchable until a
// rebuild.
func TestDeepSeekV3LogReachesTheIncrementalIndex(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	root := filepath.Join(tmp, "dsh", "sessions")
	t.Setenv("DSH_HOME", filepath.Join(tmp, "dsh"))
	t.Setenv("DEJA_DEEPSEEK_ROOT", root)

	legacy := filepath.Join("..", "..", "fixtures", "registry", "deepseek", "sessions",
		"--work-pgbouncer-lab--", "session-eaf5c9ac-0e47-4d2f-b982-8bae306062d1", "session.jsonl")
	body, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	write := func(session, name string, b []byte) string {
		t.Helper()
		dir := filepath.Join(root, "--work-pgbouncer-lab--", session)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("session-eaf5c9ac-0e47-4d2f-b982-8bae306062d1", "session.jsonl", body)

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(sessionsOf(t, dir, "deepseek")); n != 1 {
		t.Fatalf("first build indexed %d deepseek sessions, want 1", n)
	}

	v3 := []byte(`{"type":"session","version":0,"id":"session-0b7f3c1e-5a2d-4e8f-9c61-2d4b8a7e1f30","createdAt":1787420263519,"cwd":"/work/pgbouncer-lab","delegationDepth":0}
{"type":"user/message","seq":7,"time":1787420263580,"data":{"content":[{"type":"text","text":"shardneedle where does the shard map live"}],"source":{"kind":"user"},"role":"user"}}
{"type":"assistant/message","seq":20,"time":1787420263700,"data":{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"text","text":"the router reads it from config"}],"source":{"kind":"model","provider":"mini"}}}}
{"type":"turn/end","seq":21,"time":1787420263701,"data":{"turn":1}}
`)
	path := write("session-0b7f3c1e-5a2d-4e8f-9c61-2d4b8a7e1f30", "session.v3.jsonl", v3)
	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if n := len(sessionsOf(t, dir, "deepseek")); n != 2 {
		t.Fatalf("after a v3 log arrived there are %d deepseek sessions, want 2\n%s", n, progress.String())
	}
	assertFound := func(query string) {
		t.Helper()
		ss, err := Search(dir, search.Options{Query: query, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 {
			t.Errorf("%q is in %d sessions, want 1\n%s", query, len(ss), progress.String())
		}
	}
	assertFound("shardneedle")

	grown := slices.Concat(v3, []byte(`{"type":"user/message","seq":30,"time":1787420264580,"data":{"content":[{"type":"text","text":"growthneedle and after a reshard"}],"source":{"kind":"user"},"role":"user"}}
`))
	if err := os.WriteFile(path, grown, 0o644); err != nil {
		t.Fatal(err)
	}
	progress.Reset()
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	assertFound("growthneedle")
	if n := len(sessionsOf(t, dir, "deepseek")); n != 2 {
		t.Errorf("growing the v3 log left %d deepseek sessions, want 2", n)
	}
}

const (
	dshV0Header = `{"type":"session","version":0,"id":"session-7c2e9f40-3b1a-4d5e-8f60-1a2b3c4d5e6f","createdAt":1790506922540,"cwd":"/work/pgbouncer-lab","delegationDepth":0}
`
	dshV4Header = `{"type":"session","version":4,"id":"session-7c2e9f40-3b1a-4d5e-8f60-1a2b3c4d5e6f","createdAt":1790506922540,"cwd":"/work/pgbouncer-lab","isSeeded":false,"delegationDepth":0}
`
	dshOldTurn = `{"type":"user/message","seq":1,"time":1790506922600,"data":{"content":[{"type":"text","text":"oldneedle what holds the pool size"}],"source":{"kind":"user"},"role":"user"}}
`
	dshNewTurn = `{"type":"user/message","seq":2,"time":1790593322600,"data":{"content":[{"type":"text","text":"v4needle raise it to 60"}],"source":{"kind":"user"},"role":"user"}}
`
)

// writeDshLog writes body raw, or as dsh frames it: one zstd frame per line.
func writeDshLog(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	out := []byte(body)
	if strings.HasSuffix(path, ".zstd") {
		var b bytes.Buffer
		for _, line := range strings.SplitAfter(body, "\n") {
			if line == "" {
				continue
			}
			cmd := exec.CommandContext(t.Context(), "zstd", "-q", "-c")
			cmd.Stdin = strings.NewReader(line)
			frame, err := cmd.Output()
			if err != nil {
				t.Fatalf("zstd: %v", err)
			}
			b.Write(frame)
		}
		out = b.Bytes()
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

// dsh 2.0.15 migrates a session into session.v4.jsonl beside its old log and
// appends there from then on. The incremental pass has to move the session onto
// the new file: keeping the old one back as a deleted transcript held its turns
// twice and left the session on the frozen path (#4600). Cherry Studio runs dsh
// over its own store, which takes the same path.
func TestDeepSeekV4LogTakesOverFromTheLegacyLog(t *testing.T) {
	for _, tc := range []struct{ name, harness, ext string }{
		{"deepseek", "deepseek", ""},
		{"deepseek-zstd", "deepseek", ".zstd"},
		{"cherrystudio", "cherrystudio", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.ext != "" {
				if _, err := exec.LookPath("zstd"); err != nil {
					t.Skip("zstd CLI not available")
				}
			}
			tmp := t.TempDir()
			home := filepath.Join(tmp, "home")
			setHome(t, home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("DSH_HOME", filepath.Join(tmp, "dsh"))
			t.Setenv("DEJA_DEEPSEEK_ROOT", filepath.Join(tmp, "dsh", "sessions"))
			root := filepath.Join(tmp, "dsh", "sessions")
			if tc.harness == "cherrystudio" {
				root = filepath.Join(cherryStudioDir(home), "Data", "Agents", ".dsh", "sessions")
			}
			dir := filepath.Join(root, "--work-pgbouncer-lab--", "session-7c2e9f40-3b1a-4d5e-8f60-1a2b3c4d5e6f")
			writeDshLog(t, filepath.Join(dir, "session.jsonl"+tc.ext), dshV0Header+dshOldTurn)
			idx := filepath.Join(tmp, "index.db")
			if err := Ensure(idx, "", true, nil); err != nil {
				t.Fatal(err)
			}
			if n := len(sessionsOf(t, idx, tc.harness)); n != 1 {
				t.Fatalf("first build: %d %s sessions, want 1", n, tc.harness)
			}

			v4 := filepath.Join(dir, "session.v4.jsonl"+tc.ext)
			writeDshLog(t, v4, dshV4Header+dshOldTurn+dshNewTurn)
			var progress bytes.Buffer
			if err := Ensure(idx, "", false, &progress); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(progress.String(), "no longer on disk") {
				t.Errorf("the migrated log was kept as a deleted transcript:\n%s", progress.String())
			}
			for _, q := range []string{"oldneedle", "v4needle"} {
				ss, err := Search(idx, search.Options{Query: q, All: true})
				if err != nil {
					t.Fatal(err)
				}
				if len(ss) != 1 {
					t.Fatalf("%q is in %d sessions, want 1\n%s", q, len(ss), progress.String())
				}
				n := 0
				for _, m := range ss[0].Messages {
					if strings.Contains(m.Text, q) {
						n++
					}
				}
				if n != 1 {
					t.Errorf("%q is in %d messages, want 1\n%s", q, n, progress.String())
				}
			}
			metas := sessionsOf(t, idx, tc.harness)
			if len(metas) != 1 {
				t.Fatalf("%d %s sessions, want 1", len(metas), tc.harness)
			}
			for _, m := range metas {
				if m.Path != v4 {
					t.Errorf("session path = %s, want %s", m.Path, v4)
				}
			}
		})
	}
}

// cherryStudioDir is Cherry Studio's default data directory under home.
func cherryStudioDir(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "CherryStudio")
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", "CherryStudio")
	}
	return filepath.Join(home, ".config", "CherryStudio")
}

// Cursor keeps each CLI chat in a directory of its own, so deleting a chat
// takes the directory with the transcript, and the pass read that as a store
// that went away and dropped the session — while the same deletion of a
// Claude transcript keeps it (#2970, #4195).
func TestADeletedSessionDirectoryStaysInTheIndex(t *testing.T) {
	tmp := t.TempDir()
	cli := filepath.Join(tmp, "cursor-cli")
	chats := filepath.Join(cli, "projects", "Users-me-app", "agent-transcripts")
	write := func(id, text string) {
		p := filepath.Join(chats, id, id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(`{"role":"user","message":{"content":[{"type":"text","text":"`+text+`"}]}}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gone := "bbbbbbbb-1111-4222-8333-444444444444"
	write(gone, "the quarnex cache went stale")
	write("cccccccc-1111-4222-8333-444444444444", "beta stable")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "no-claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", cli)
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "cursor", false, nil); err != nil {
		t.Fatal(err)
	}

	if err := os.RemoveAll(filepath.Join(chats, gone)); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := Ensure(dir, "cursor", false, &log); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, search.Options{Query: "quarnex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != gone {
		t.Fatalf("a chat deleted with its directory left the index: %#v\nlog: %s", ss, log.String())
	}
	if !strings.Contains(log.String(), "still searchable") || strings.Contains(log.String(), "not mounted") {
		t.Errorf("the pass did not say the chat was kept:\n%s", log.String())
	}

	// A rebuild keeps it too: it reads the kept records back from the old
	// store rather than from a transcript that is no longer there.
	if err := Ensure(dir, "cursor", true, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "quarnex"}); len(ss) != 1 {
		t.Fatalf("a rebuild dropped the deleted chat: %#v", ss)
	}

	// The store itself going away is still an uninstall or a move.
	if err := os.RemoveAll(filepath.Join(cli, "projects")); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "cursor", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "stable"}); len(ss) != 0 {
		t.Fatalf("a store that went away whole stayed in the index: %#v", ss)
	}
}

// Only a directory named for one session counts. An encoded working directory
// under a store root carries a UUID whenever the directory did, and deleting
// that folder is a project going away, as before.
func TestSessionDirNames(t *testing.T) {
	const id = "5b0c1a7e-2f4d-4c1b-9a3e-7d2f1c0b9e8a"
	for name, want := range map[string]bool{
		id:                        true, // Cursor, Copilot CLI, Antigravity, a Claude sidecar
		"session_" + id:           true, // Kimi
		"session-" + id:           true, // DeepSeek
		"sess_" + id:              true, // Kiro
		"1791454692202_zb19s":     true, // Cline CLI
		"1791454692202":           false,
		"-private-var-tmp-" + id:  false,
		"Users-me-" + id + "-app": false,
		"projects":                false,
	} {
		if got := sessionDirName.MatchString(name); got != want {
			t.Errorf("%q: %v, want %v", name, got, want)
		}
	}
}

// A chat moved with its directory and edited on the way is not caught by the
// rename pairing, and keeping the old copy as a deletion made it its own
// second copy.
func TestAMovedSessionDirectoryIsNotKeptTwice(t *testing.T) {
	tmp := t.TempDir()
	cli := filepath.Join(tmp, "cursor-cli")
	id := "bbbbbbbb-1111-4222-8333-444444444444"
	at := func(project, text string) {
		p := filepath.Join(cli, "projects", project, "agent-transcripts", id, id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(`{"role":"user","message":{"content":[{"type":"text","text":"`+text+`"}]}}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	at("Users-me-app", "the vorplex migration stalled")
	at("Users-me-other", "beta stable")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "no-claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", cli)
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "cursor", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(cli, "projects", "Users-me-app", "agent-transcripts", id)); err != nil {
		t.Fatal(err)
	}
	at("Users-me-app2", "the vorplex migration resumed")
	if err := Ensure(dir, "cursor", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "stalled"}); len(ss) != 0 {
		t.Fatalf("the moved chat's old copy stayed beside the new one: %#v", ss)
	}
	if ss, _ := Search(dir, search.Options{Query: "resumed"}); len(ss) != 1 {
		t.Fatalf("the moved chat is not there once: %#v", ss)
	}
	// Nor does a rebuild bring the old copy back.
	if err := Ensure(dir, "cursor", true, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "stalled"}); len(ss) != 0 {
		t.Fatalf("a rebuild kept the moved chat's old copy: %#v", ss)
	}
}

// Claude Code deletes a transcript once it is older than cleanupPeriodDays
// (30 by default), and the next incremental pass dropped the session with it:
// the one client-side housekeeping deja exists to outlast took the index down
// with the file. A file that is gone while its store is still there is that
// cleanup, or a deletion by hand — and `deja forget` is the deliberate path,
// so the index keeps what it indexed (#2970).
func TestADeletedTranscriptStaysInTheIndex(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-me-deja-vu")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	s1 := filepath.Join(proj, "s1.jsonl")
	s2 := filepath.Join(proj, "s2.jsonl")
	if err := os.WriteFile(s1, []byte(`{"type":"user","sessionId":"s1","timestamp":"2026-06-02T03:04:05Z","message":{"role":"user","content":"the zorblax pool deadlocked"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s2, []byte(`{"type":"user","sessionId":"s2","timestamp":"2026-08-02T03:04:05Z","message":{"role":"user","content":"beta stable"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}

	// The cleanup: one file gone, the store around it still there.
	if err := os.Remove(s1); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := Ensure(dir, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, search.Options{Query: "zorblax"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "s1" {
		t.Fatalf("a transcript the client deleted left the index with it: %#v\nlog: %s", ss, log.String())
	}
	if !strings.Contains(log.String(), "still searchable") {
		t.Errorf("the pass kept the session and said nothing about it:\n%s", log.String())
	}
	// And it stays kept on the pass after that, when there is nothing else to
	// do. That run is the fast one: the index reads as fresh and says nothing.
	log.Reset()
	if err = Ensure(dir, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	if log.Len() > 0 {
		t.Errorf("a run with nothing new repeated itself:\n%s", log.String())
	}
	if ss, _ := Search(dir, search.Options{Query: "zorblax"}); len(ss) != 1 {
		t.Fatalf("the kept session went away on the following pass: %#v", ss)
	}
	// And on the pass that appends to a neighbour — the cheap path, which
	// parses nothing but the grown file — the kept session is still there
	// and the pass still says so.
	f, err := os.OpenFile(s2, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"assistant","sessionId":"s2","timestamp":"2026-08-02T03:05:05Z","message":{"role":"assistant","content":"gamma appended"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	log.Reset()
	if err := Ensure(dir, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "zorblax"}); len(ss) != 1 {
		t.Fatalf("the kept session went away on an append pass: %#v", ss)
	}
	if !strings.Contains(log.String(), "still searchable") {
		t.Errorf("the append pass kept the session and said nothing about it:\n%s", log.String())
	}
	// A session with the same id arriving from another project is a collision
	// (#699), not the kept transcript being renamed: both stay.
	other := filepath.Join(claudeRoot, "-Users-me-other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "s1.jsonl"), []byte(`{"type":"user","sessionId":"s1","timestamp":"2026-08-03T03:04:05Z","message":{"role":"user","content":"unrelated quux work"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "zorblax"}); len(ss) != 1 {
		t.Fatalf("a same-id session from another project took the kept one with it: %#v", ss)
	}
	if ss, _ := Search(dir, search.Options{Query: "quux"}); len(ss) != 1 {
		t.Fatalf("the colliding session from the other project was not indexed: %#v", ss)
	}

	// The other case is unchanged: the whole store gone is not a cleanup —
	// it is an uninstall, a move, or a disk that is not there — and a tree
	// that is not a mount point is dropped as before.
	if err := os.RemoveAll(claudeRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(claudeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "stable"}); len(ss) != 0 {
		t.Fatalf("a store that went away whole was kept: %#v", ss)
	}
}

// A directory the process cannot read takes its sessions out of recall, and
// nothing downstream could attribute it: the walk dropped EACCES, and a
// diagnostic path with no transcript extension matched no harness (#818).
func TestUnreadableDirectoryIsAttributedToItsHarness(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	root, dir := allHarnessEnv(t)
	write(t, filepath.Join(root, "claude", "-tmp-open", "s.jsonl"),
		claudeLine("s1", "2026-01-02T03:04:05Z", "manneedle here"))
	locked := filepath.Join(root, "claude", "-tmp-locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(locked, "s.jsonl"),
		claudeLine("s2", "2026-01-03T03:04:05Z", "manneedle in the locked half"))
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	if err := EnsureForSearch(dir, search.Options{Query: "manneedle", All: true}, true, nil); err != nil {
		t.Fatal(err)
	}
	health := IngestHealth(dir)
	e, ok := health["claude"]
	if !ok {
		t.Fatalf("no ingest health for claude: %v", health)
	}
	if e.FailedFiles == 0 {
		t.Errorf("the unreadable directory was not counted: %+v", e)
	}
	if e.LastError == "" {
		t.Errorf("no error text recorded: %+v", e)
	}
}

// The mapping a directory needs: transcript matchers reject it, so what a
// transcript inside it would be is the question that answers.
func TestHarnessForPathHandlesDirectories(t *testing.T) {
	root, _ := allHarnessEnv(t)
	claudeDir := filepath.Join(root, "claude", "-tmp-p")
	if got := harnessForPath(claudeDir); got == "" {
		t.Errorf("a directory under the claude root mapped to no harness")
	}
	if got := harnessForPath(filepath.Join(claudeDir, "s.jsonl")); got == "" {
		t.Errorf("a transcript under the claude root mapped to no harness")
	}
	if got := harnessForPath(filepath.Join(t.TempDir(), "elsewhere")); got != "" {
		t.Errorf("an unrelated directory mapped to %q", got)
	}
}

// fellIndex indexes one single-message session per entry, id -> project, text.
func fellIndex(t *testing.T, sessions [][3]string) string {
	t.Helper()
	dir := filepath.Join(hermeticIndexEnv(t), "idx")
	ss := make([]model.Session, 0, len(sessions))
	for i, s := range sessions {
		ss = append(ss, model.Session{
			ID: s[0], Harness: "claude", Project: s[1], Updated: time.Now().Add(-time.Duration(i) * time.Minute),
			Messages: []model.Message{{Role: "user", Text: s[2]}},
		})
	}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir
}

var fellCorpus = [][3]string{
	{"a1", "alpha", "the walrus importer stalled on the third batch"},
	{"a2", "alpha", "walrus dashboards were slow on monday"},
	{"a3", "alpha", "lunch order for the offsite"},
	{"b1", "beta", "billing tables moved to the new cluster"},
	{"b2", "beta", "billing export runs nightly now"},
	{"b3", "beta", "walrus logo for the slides"},
	{"me", "alpha", "the walrus billing pelican run broke again"},
}

func resultIDs(r SearchResult) []string {
	out := make([]string, 0, len(r.Sessions))
	for _, s := range r.Sessions {
		out = append(out, s.ID)
	}
	return out
}

// Recall asks the whole machine first and the project after it. When every
// rung above the ranked tier came back empty for the machine, the project's
// search starts at the ranked tier and answers the same as the whole ladder.
func TestANarrowerSearchStartsWhereTheMachineFell(t *testing.T) {
	dir := fellIndex(t, fellCorpus)
	mine := map[string]bool{"me": true}
	machine, err := SearchDetailed(dir, search.Options{Query: "walrus billing pelican", All: true, ExcludeSessions: mine})
	if err != nil {
		t.Fatal(err)
	}
	if machine.Fell == 0 || machine.Tier != query.TierRelevance {
		t.Fatalf("the machine's search did not fall to the ranked tier: fell=%d tier=%s %v", machine.Fell, machine.Tier, resultIDs(machine))
	}
	o := search.Options{Query: "walrus billing pelican", All: true, ExcludeSessions: mine, Projects: []string{"alpha"}}
	whole, err := SearchDetailed(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	o.FellThrough = machine.Fell
	short, err := SearchDetailed(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(whole.Sessions) == 0 || !slices.Equal(resultIDs(whole), resultIDs(short)) || whole.Tier != short.Tier {
		t.Fatalf("starting at the ranked tier changed the answer: ladder %v (%s), from the ranked tier %v (%s)",
			resultIDs(whole), whole.Tier, resultIDs(short), short.Tier)
	}
}

// A search a rung answers has not fallen, and a narrower one must walk the
// ladder itself.
func TestAnAnsweredSearchHasNotFallen(t *testing.T) {
	dir := fellIndex(t, fellCorpus)
	r, err := SearchDetailed(dir, search.Options{Query: "walrus importer", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.Fell != 0 || len(r.Sessions) == 0 {
		t.Fatalf("an exact answer is marked as fallen: fell=%d %v", r.Fell, resultIDs(r))
	}
}

// `gemini --resume <id>` appends the resumed turns to the original transcript
// and leaves a second file with the same sessionId: a header and a $set holding
// only the <session_context> preamble, which deja strips. That stub took the
// id, and since it held nothing the row was dropped — every resumed session
// went missing from search, show and last (#4213).

const resumeID = "008140a7-3349-4cca-bc66-b32418429ebf"

const resumePreamble = `{"$set":{"messages":[{"id":"ctx","timestamp":"%s","type":"user","content":[{"text":"<session_context>\nThis is the Gemini CLI. We are setting up the context for our chat.\n</session_context>"}]}]}}`

func geminiResumeHeader(start string) string {
	return `{"sessionId":"` + resumeID + `","projectHash":"bc04","startTime":"` + start + `","lastUpdated":"` + start + `","kind":"main"}`
}

func geminiPreamble(at string) string {
	return strings.Replace(resumePreamble, "%s", at, 1)
}

// The original transcript before the resume.
func geminiOriginal() string {
	return strings.Join([]string{
		geminiResumeHeader("2026-10-01T13:04:39.847Z"),
		geminiPreamble("2026-10-01T13:04:39.849Z"),
		`{"id":"u1","timestamp":"2026-10-01T13:04:40.000Z","type":"user","content":[{"text":"the users table needs an email column"}]}`,
		`{"id":"g1","timestamp":"2026-10-01T13:04:44.698Z","type":"gemini","content":"Add the email column as nullable first."}`,
		`{"$set":{"lastUpdated":"2026-10-01T13:04:44.702Z"}}`,
	}, "\n") + "\n"
}

// What the resume appends to the original.
func geminiResumedTail() string {
	return strings.Join([]string{
		`{"$set":{"sessionId":"` + resumeID + `"}}`,
		`{"id":"u2","timestamp":"2026-10-01T13:07:52.492Z","type":"user","content":[{"text":"what about existing rows"}]}`,
		`{"id":"g2","timestamp":"2026-10-01T13:07:55.100Z","type":"gemini","content":"Add NOT NULL DEFAULT '' to the schema migration."}`,
		`{"$set":{"lastUpdated":"2026-10-01T13:07:55.101Z"}}`,
	}, "\n") + "\n"
}

// The file the resume leaves behind.
func geminiStub() string {
	return geminiResumeHeader("2026-10-01T13:07:52.403Z") + "\n" + geminiPreamble("2026-10-01T13:07:52.409Z") + "\n"
}

func geminiChats() string {
	return filepath.Join(os.Getenv("DEJA_GEMINI_ROOT"), "tmp", "proj", "chats")
}

// assertResumedSessionHeld checks the session is findable, filed under the
// transcript that holds it, and not reported as two conversations sharing an id.
func assertResumedSessionHeld(t *testing.T, dir, realPath, query string) {
	t.Helper()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	row, ok := m.Sessions["gemini:"+resumeID]
	if !ok {
		t.Fatalf("the resumed session has no row; rows: %v", m.Sessions)
	}
	if row.Path != realPath {
		t.Errorf("row is filed under %s, want the transcript holding the conversation %s", row.Path, realPath)
	}
	if row.Shared {
		t.Error("the stub was counted as a second conversation sharing the id")
	}
	ss, err := Search(dir, search.Options{Query: query, All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != resumeID {
		t.Fatalf("%q found %d sessions, want the resumed one", query, len(ss))
	}
}

func TestGeminiResumeStubDoesNotTakeTheSessionFullBuild(t *testing.T) {
	for _, tc := range []struct{ name, real, stub string }{
		// The names Gemini gives them: the stub's stamp sorts after.
		{"stub after", "session-2026-10-01T13-04-008140a7.jsonl", "session-2026-10-01T13-07-008140a7.jsonl"},
		// And the other way round, so the order the files are read in does
		// not decide it.
		{"stub first", "session-b-008140a7.jsonl", "session-a-008140a7.jsonl"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			realPath := filepath.Join(geminiChats(), tc.real)
			write(t, realPath, geminiOriginal()+geminiResumedTail())
			write(t, filepath.Join(geminiChats(), tc.stub), geminiStub())
			dir := filepath.Join(tmp, "idx")
			if err := Ensure(dir, "", false, nil); err != nil {
				t.Fatal(err)
			}
			if n := ReportCollisions(); n != 0 {
				t.Errorf("reported %d id collisions for one resumed session", n)
			}
			// The stub is still a transcript that held nothing, whichever
			// order it was read in.
			if n := ReportEmptySessions(); n != 1 {
				t.Errorf("reported %d empty transcripts, want the stub", n)
			}
			assertResumedSessionHeld(t, dir, realPath, "schema migration")
		})
	}
}

// The order it happens in: the session is indexed, then the resume appends to
// it and leaves the stub, and the next pass sees both.
func TestGeminiResumeStubDoesNotTakeTheSessionIncremental(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	realPath := filepath.Join(geminiChats(), "session-2026-10-01T13-04-008140a7.jsonl")
	write(t, realPath, geminiOriginal())
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	assertResumedSessionHeld(t, dir, realPath, "email column")

	appendFile(t, realPath, geminiResumedTail())
	write(t, filepath.Join(geminiChats(), "session-2026-10-01T13-07-008140a7.jsonl"), geminiStub())
	bumpMtime(t, realPath)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := ReportCollisions(); n != 0 {
		t.Errorf("reported %d id collisions for one resumed session", n)
	}
	assertResumedSessionHeld(t, dir, realPath, "schema migration")
}

// The stub on its own in a later pass, with the original unchanged, and the
// reverse: a stub already indexed when the original arrives.
func TestGeminiResumeStubAloneInALaterPass(t *testing.T) {
	t.Run("stub after", func(t *testing.T) {
		tmp := hermeticIndexEnv(t)
		realPath := filepath.Join(geminiChats(), "session-b-008140a7.jsonl")
		write(t, realPath, geminiOriginal()+geminiResumedTail())
		dir := filepath.Join(tmp, "idx")
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(geminiChats(), "session-a-008140a7.jsonl"), geminiStub())
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		if n := ReportCollisions(); n != 0 {
			t.Errorf("reported %d id collisions for one resumed session", n)
		}
		assertResumedSessionHeld(t, dir, realPath, "schema migration")
	})
	t.Run("stub first", func(t *testing.T) {
		tmp := hermeticIndexEnv(t)
		// Something else indexed first, so the stub arrives in an update
		// rather than the first build.
		write(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "other.jsonl"),
			claudeLine("s-other", "2026-01-02T03:04:05Z", "the exporter retries without a pause"))
		dir := filepath.Join(tmp, "idx")
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(geminiChats(), "session-z-008140a7.jsonl"), geminiStub())
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		realPath := filepath.Join(geminiChats(), "session-b-008140a7.jsonl")
		write(t, realPath, geminiOriginal()+geminiResumedTail())
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		if n := ReportCollisions(); n != 0 {
			t.Errorf("reported %d id collisions for one resumed session", n)
		}
		assertResumedSessionHeld(t, dir, realPath, "schema migration")
	})
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func bumpMtime(t *testing.T, path string) {
	t.Helper()
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

// The original deleted — Gemini's own cleanup, or by hand — in the same pass
// the stub arrives beside it. The stub is not the transcript moving: the
// rename rule took it for one, the deleted file's records went, and the row
// was left on a file with nothing in it, even after a rebuild.
func TestGeminiResumeStubIsNotARenameOfADeletedTranscript(t *testing.T) {
	// Alone, the pass takes the append path; with another Gemini transcript
	// changed beside it, the replacement path and its rename rule.
	for _, withChange := range []bool{false, true} {
		t.Run(map[bool]string{false: "append path", true: "replacement path"}[withChange], func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			realPath := filepath.Join(geminiChats(), "session-2026-10-01T13-04-008140a7.jsonl")
			write(t, realPath, geminiOriginal()+geminiResumedTail())
			other := filepath.Join(geminiChats(), "session-2026-09-30T10-00-0badc0de.jsonl")
			otherHeader := `{"sessionId":"0badc0de","startTime":"2026-09-30T10:00:00Z","lastUpdated":"2026-09-30T10:00:00Z"}` + "\n"
			write(t, other, otherHeader+`{"id":"o1","timestamp":"2026-09-30T10:00:01Z","type":"user","content":[{"text":"the cache warms on boot"}]}`+"\n")
			dir := filepath.Join(tmp, "idx")
			if err := Ensure(dir, "", false, nil); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(realPath); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(geminiChats(), "session-2026-10-01T13-07-008140a7.jsonl"), geminiStub())
			if withChange {
				appendFile(t, other, `{"id":"o2","timestamp":"2026-09-30T10:00:02Z","type":"gemini","content":"It does."}`+"\n")
				bumpMtime(t, other)
			}
			if err := Ensure(dir, "", false, nil); err != nil {
				t.Fatal(err)
			}
			assertResumedSessionHeld(t, dir, realPath, "schema migration")
			if err := Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			assertResumedSessionHeld(t, dir, realPath, "schema migration")
		})
	}
}

// The empty-transcript rule is for pairs that would be reported as a clash.
// A store pair attributeSession already knows keeps its own answer, and a held
// row with text but no words (emoji, punctuation) is still a conversation.
func TestClaimSessionOnlyOverridesAClash(t *testing.T) {
	empty := model.Session{Harness: "goose", ID: "g", Path: "/h/.local/share/goose/sessions/sessions.db",
		Messages: []model.Message{{Role: "user", Text: "  "}}}
	held := SessionMeta{Harness: "goose", ID: "g", Path: "/h/.local/share/goose/sessions/g.jsonl", Words: 3}
	if owns, collided := claimSession(held, empty); !owns || collided {
		t.Errorf("goose db over its jsonl: owns=%v collided=%v, want the store rule (owns, no clash)", owns, collided)
	}

	text := model.Session{Harness: "gemini", ID: "x", Path: "/c/session-a-x.jsonl",
		Messages: []model.Message{{Role: "user", Text: "the pool deadlocked"}}}
	emoji := SessionMeta{Harness: "gemini", ID: "x", Path: "/c/session-b-x.jsonl", Words: 0}
	if _, collided := claimSession(emoji, text); !collided {
		t.Error("a held row of emoji gave up the id without a clash being reported")
	}
	stub := SessionMeta{Harness: "gemini", ID: "x", Path: "/c/session-b-x.jsonl", NoText: true}
	if owns, collided := claimSession(stub, text); !owns || collided {
		t.Errorf("over a row with no text: owns=%v collided=%v, want owns, no clash", owns, collided)
	}
}

// The original deleted while the stub beside it stays, then the stub gains a
// turn of its own. Now an arrival with text in the same directory, it was
// taken for the deleted transcript moving, and the transcript's records went
// for good — a rebuild has nothing to bring them back from.
func TestGeminiResumedStubIsNotARenameOfTheDeletedTranscript(t *testing.T) {
	for _, withChange := range []bool{false, true} {
		t.Run(map[bool]string{false: "append path", true: "replacement path"}[withChange], func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			realPath := filepath.Join(geminiChats(), "session-2026-10-01T13-04-008140a7.jsonl")
			stub := filepath.Join(geminiChats(), "session-2026-10-01T13-07-008140a7.jsonl")
			write(t, realPath, geminiOriginal()+geminiResumedTail())
			write(t, stub, geminiStub())
			other := filepath.Join(geminiChats(), "session-2026-09-30T10-00-0badc0de.jsonl")
			write(t, other, `{"sessionId":"0badc0de","startTime":"2026-09-30T10:00:00Z","lastUpdated":"2026-09-30T10:00:00Z"}`+"\n"+
				`{"id":"o1","timestamp":"2026-09-30T10:00:01Z","type":"user","content":[{"text":"the cache warms on boot"}]}`+"\n")
			dir := filepath.Join(tmp, "idx")
			if err := Ensure(dir, "", false, nil); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(realPath); err != nil {
				t.Fatal(err)
			}
			if err := Ensure(dir, "", false, nil); err != nil {
				t.Fatal(err)
			}
			appendFile(t, stub, `{"id":"u3","timestamp":"2026-10-01T13:09:00.000Z","type":"user","content":[{"text":"backfill the tenants table next"}]}`+"\n")
			bumpMtime(t, stub)
			if withChange {
				appendFile(t, other, `{"id":"o2","timestamp":"2026-09-30T10:00:02Z","type":"gemini","content":"It does."}`+"\n")
				bumpMtime(t, other)
			}
			found := func(stage string) {
				t.Helper()
				for _, q := range []string{"schema migration", "tenants table"} {
					ss, err := Search(dir, search.Options{Query: q, All: true})
					if err != nil {
						t.Fatal(err)
					}
					if len(ss) != 1 || ss[0].ID != resumeID {
						t.Errorf("%s: %q found %d sessions, want the resumed one", stage, q, len(ss))
					}
				}
			}
			if err := Ensure(dir, "", false, nil); err != nil {
				t.Fatal(err)
			}
			found("after the stub grew")
			if err := Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			found("after a rebuild")
		})
	}
}

// The stub indexed on its own, then the transcript arrives. The row changes
// hands; folding the transcript into the stub's row counted the stub's
// preamble on top, one more message than a rebuild of the same files.
func TestGeminiTranscriptTakingAStubRowMatchesARebuild(t *testing.T) {
	// With and without the resumed turns: without them the transcript ends
	// before the stub was written, and the row's span has to keep the stub's.
	// The stub sorts first, so the rebuild reads it first and merges its span.
	// The stub's row has no record, so it is set aside rather than listed,
	// whichever pass read the stub (#5563).
	for name, transcript := range map[string]string{"resumed": geminiOriginal() + geminiResumedTail(), "ends before the stub": geminiOriginal()} {
		for _, by := range []string{"append", "re-read", "rebuild"} {
			t.Run(name+", stub read by "+by, func(t *testing.T) { transcriptTakesStubRow(t, transcript, by) })
		}
	}
}

func transcriptTakesStubRow(t *testing.T, realPath, by string) {
	t.Helper()
	tmp := hermeticIndexEnv(t)
	other := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "other.jsonl")
	write(t, other, claudeLine("s-other", "2026-01-02T03:04:05Z", "the exporter retries without a pause")+
		claudeLine("s-other", "2026-01-02T03:04:06Z", "and the backoff is capped"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(geminiChats(), "session-a-008140a7.jsonl"), geminiStub())
	if by == "re-read" {
		// A file that shrank sends the pass to the re-read path.
		write(t, other, claudeLine("s-other", "2026-01-02T03:04:05Z", "the exporter retries without a pause"))
	}
	if err := Ensure(dir, "", by == "rebuild", nil); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(geminiChats(), "session-b-008140a7.jsonl"), realPath)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	row := func() SessionMeta {
		t.Helper()
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		return m.Sessions["gemini:"+resumeID]
	}
	got := row()
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	want := row()
	if got.Counted != want.Counted || got.Words != want.Words || got.LastMsg != want.LastMsg || got.NoText != want.NoText ||
		!got.Started.Equal(want.Started) || !got.Updated.Equal(want.Updated) || got.Path != want.Path || !slices.Equal(got.Copies, want.Copies) {
		t.Errorf("row after the transcript arrived: Counted=%d Words=%d Started=%v Updated=%v Path=%s Copies=%v; a rebuild gives Counted=%d Words=%d Started=%v Updated=%v Path=%s Copies=%v",
			got.Counted, got.Words, got.Started, got.Updated, got.Path, got.Copies, want.Counted, want.Words, want.Started, want.Updated, want.Path, want.Copies)
	}
}

// A move whose last command got its result in between: the re-read appends
// "  → exit N" to the message the row fingerprinted, and the move was taken
// for a second file under the same id — the row stayed on the dead path,
// marked shared, and both copies of the command stayed in the records.
func TestAMoveThatAddsAnExitStatusIsStillAMove(t *testing.T) {
	for _, tc := range []struct {
		name            string
		before, after   func(t *testing.T) (old, moved string)
		key, staleQuery string
	}{
		{name: "codex rollout compressed in place", key: "codex:cx-1", staleQuery: "go test",
			before: func(t *testing.T) (string, string) {
				t.Helper()
				if _, err := exec.LookPath("zstd"); err != nil {
					t.Skip("zstd not installed")
				}
				dir := filepath.Join(os.Getenv("DEJA_CODEX_ROOT"), "sessions", "2026", "07", "31")
				p := filepath.Join(dir, "rollout-2026-07-31T00-00-00-cx-1.jsonl")
				write(t, p, codexRolloutHead)
				return p, p + ".zst"
			},
			after: func(t *testing.T) (string, string) {
				t.Helper()
				dir := filepath.Join(os.Getenv("DEJA_CODEX_ROOT"), "sessions", "2026", "07", "31")
				p := filepath.Join(dir, "rollout-2026-07-31T00-00-00-cx-1.jsonl")
				appendFile(t, p, `{"timestamp":"2026-07-31T00:00:03Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"Process exited with code 1\nOutput:\nFAIL\n"}}`+"\n")
				if out, err := exec.CommandContext(t.Context(), "zstd", "-q", "--rm", p).CombinedOutput(); err != nil {
					t.Fatalf("zstd: %v %s", err, out)
				}
				return p, p + ".zst"
			}},
		{name: "gemini json rewritten as jsonl", key: "gemini:sess-mv-1", staleQuery: "git log",
			before: func(t *testing.T) (string, string) {
				t.Helper()
				p := filepath.Join(geminiChats(), "session-2026-10-01T12-51-sess-mv-1.json")
				write(t, p, geminiJSONBeforeResult)
				return p, strings.TrimSuffix(p, ".json") + ".jsonl"
			},
			after: func(t *testing.T) (string, string) {
				t.Helper()
				p := filepath.Join(geminiChats(), "session-2026-10-01T12-51-sess-mv-1.json")
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
				write(t, p+"l", geminiJSONLWithResult)
				return p, p + "l"
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			tc.before(t)
			// An unrelated transcript that shrinks, so the pass takes the
			// replacement path and its rename rule.
			other := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "other.jsonl")
			write(t, other, claudeLine("s-other", "2026-01-02T03:04:05Z", "the exporter retries without a pause")+
				claudeLine("s-other", "2026-01-02T03:04:06Z", "and the backoff is capped"))
			dir := filepath.Join(tmp, "idx")
			if err := Ensure(dir, "", false, nil); err != nil {
				t.Fatal(err)
			}
			old, moved := tc.after(t)
			write(t, other, claudeLine("s-other", "2026-01-02T03:04:05Z", "the exporter retries without a pause"))
			if err := Ensure(dir, "", false, nil); err != nil {
				t.Fatal(err)
			}
			m, err := readManifest(dir)
			if err != nil {
				t.Fatal(err)
			}
			row := m.Sessions[tc.key]
			if row.Path != moved || row.Shared {
				t.Errorf("row on %s shared=%v, want it moved to %s and not shared", row.Path, row.Shared, moved)
			}
			if _, ok := m.Files[old]; ok {
				t.Errorf("the old path %s is still held", old)
			}
			var cmds []string
			recs, err := ReadRecords(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range recs {
				if r.Record.Key == tc.key && strings.Contains(r.Record.Text, tc.staleQuery) {
					cmds = append(cmds, r.Record.Text)
				}
			}
			if len(cmds) != 1 || !strings.Contains(cmds[0], "→ exit") {
				t.Errorf("command records = %q, want the one with its exit status", cmds)
			}
		})
	}
}

const codexRolloutHead = `{"timestamp":"2026-07-31T00:00:00Z","type":"session_meta","payload":{"id":"cx-1","session_id":"cx-1","cwd":"/w/app"}}
{"timestamp":"2026-07-31T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"run the tests"}]}}
{"timestamp":"2026-07-31T00:00:02Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{\"cmd\":\"go test ./...\",\"workdir\":\"/w/app\"}"}}
`

const geminiShellCall = `{"id":"run_shell_command__1","name":"run_shell_command","args":{"command":"git log --oneline -1"}`

const geminiJSONBeforeResult = `{"sessionId":"sess-mv-1","projectHash":"abc","startTime":"2026-10-01T12:51:00.000Z","lastUpdated":"2026-10-01T12:51:02.000Z","messages":[
{"id":"u1","timestamp":"2026-10-01T12:51:01.000Z","type":"user","content":[{"text":"show the last commit"}]},
{"id":"g1","timestamp":"2026-10-01T12:51:02.000Z","type":"gemini","content":"","toolCalls":[` + geminiShellCall + `,"status":"executing"}]}]}`

const geminiJSONLWithResult = `{"sessionId":"sess-mv-1","projectHash":"abc","startTime":"2026-10-01T12:51:00.000Z","lastUpdated":"2026-10-01T12:51:03.000Z","kind":"main"}
{"id":"u1","timestamp":"2026-10-01T12:51:01.000Z","type":"user","content":[{"text":"show the last commit"}]}
{"id":"g1","timestamp":"2026-10-01T12:51:02.000Z","type":"gemini","content":"","toolCalls":[` + geminiShellCall + `,"result":[{"functionResponse":{"id":"run_shell_command__1","name":"run_shell_command","response":{"output":"Output: fatal: no commits\nExit Code: 128"}}}],"status":"success"}]}
`

func TestGiveUpLineKeepsReportsAndDropsEverythingElse(t *testing.T) {
	reports := []string{
		"reverted the retry cap, it made the queue worse",
		"откатили пул соединений, стало хуже",
		"we backed out the migration and went with the view instead",
		"abandoned the sidecar approach and went back to the cron job",
		"gave up on the websocket path, polling it is",
	}
	for _, l := range reports {
		if _, ok := GiveUpLine(l); !ok {
			t.Errorf("a report of backing something out is not detected: %q", l)
		}
	}
	notReports := []string{
		// A question is the moment before the decision, not the decision.
		"should we have reverted the retry cap?",
		// Source that talks about reverting is code, not a report.
		`log.Printf("reverted %s", name)`,
		"// reverted in the previous release",
		// Too short to say anything, and too long to be a statement.
		"reverted",
		// Ordinary failure talk describes the world, not a decision about it —
		// this is how nearly every debugging session opens, and it must not
		// mark the session as a dead end.
		"the webhook retry doesn't work when the queue is full",
		"if that does not work, we can add an index on created_at",
		"the sidecar approach did not work, still digging",
		"не сработал ретрай при переполнении очереди, смотрю почему",
		"the build failed on the arm runner again",
	}
	for _, l := range notReports {
		if _, ok := GiveUpLine(l); ok {
			t.Errorf("this is not a report of backing something out: %q", l)
		}
	}
}

// Tool output is full of the word: git prints it, deja prints it, and a diff
// carries whatever the file said. Only what someone actually said counts.
func TestGaveUpReadsSpeechAndNotToolOutput(t *testing.T) {
	fromOutput := []model.Message{
		{Role: "tool-output", Text: "HEAD is now at 8f2c19a Revert \"cap retries\"\nreverted 1 commit"},
	}
	if gaveUp(fromOutput) {
		t.Error("git's own output marks the session as a dead end")
	}
	fromSpeech := []model.Message{
		{Role: "assistant", Text: "capped the retries\nreverted that, it made the queue worse"},
	}
	if !gaveUp(fromSpeech) {
		t.Error("the assistant saying it reverted the change is not detected")
	}
}

// P0-9/10/11: real prose reports carry quotes, issue refs and URLs, and can end
// in a follow-up question; a byte budget must not halve the room for Cyrillic.
func TestGiveUpLineDetectsRealProseReports(t *testing.T) {
	reports := []string{
		"reverted the change from #1143 and shipped the view instead",
		"rolled back after reading github.com/foo/bar//issues/3, it deadlocked",
		`reverted the "fast path" change because it broke ordering`,
		"откатили, но не помогло, что дальше?", // report + question
		"откатили миграцию пула соединений на постгресе, потому что она держала лок на всю таблицу и всё вставало намертво", // long Cyrillic
	}
	for _, l := range reports {
		if _, ok := GiveUpLine(l); !ok {
			t.Errorf("a real report was rejected: %q", l)
		}
	}
	notReports := []string{
		"should we revert this change?",       // pure question
		`log.Printf("reverted %s", name)`,     // code with string arg
		"// reverted in the previous release", // comment
	}
	for _, l := range notReports {
		if _, ok := GiveUpLine(l); ok {
			t.Errorf("a non-report was accepted: %q", l)
		}
	}
}

// Precision review: a reversal that did not happen — negated, conditional,
// future, or the reflexive Russian verb — must not mark the session.
func TestGiveUpLineRejectsUnrealisedReversals(t *testing.T) {
	notReports := []string{
		// Negation.
		"we haven't reverted the migration yet",
		"we never gave up on the retry approach",
		"не отказались от идеи с пулом соединений",
		"он сказал не откатывать это ни в коем случае",
		// Conditional / future.
		"if this fails we just roll it back, no big deal",
		"we could roll it back but we won't do that now",
		"I'll be reverting the config change tomorrow",
		// Question with an internal separator.
		"did we roll it back, or is it still live?",
		"should we have reverted, or patched forward?",
		// Reflexive Russian verb (all genders/numbers), not a reversal action.
		"мяч откатился под диван и застрял там надолго",
		"деталь откатилась назад и упала со стола",
		"колесо откатилось в сторону от станка",
		"камни откатились вниз по склону после дождя",
		// A diff removed-line is not a report of a reversal.
		"- reverted the old migration helper",
		// A copula question without a "?" is still a question, not a report.
		"was that rolled back",
		"is that reverted yet",
		"were those changes reverted",
		// Metalinguistic mentions of reversal tokens in backticks or quotes.
		"the filter checks for `reverted` or «откатили» in assistant turns",
		`do not match "rolled back" when quoted as a literal token`,
	}
	for _, l := range notReports {
		if _, ok := GiveUpLine(l); ok {
			t.Errorf("an unrealised or non-reversal was marked: %q", l)
		}
	}
	// And genuine reports still pass, including a report that ends in a
	// separate follow-up question.
	reports := []string{
		"reverted the retry cap, it made the queue worse",
		"откатили, но не помогло, что дальше?",
		"we backed out the migration and went with the view instead",
		"* reverted the caching layer, it broke ordering",
		"> rolled back the migration after the DBA complained",
		// A modal AFTER the phrase belongs to another verb — still a report.
		"reverted it since it would corrupt state otherwise",
		"reverted the index because a full scan could deadlock",
		// Subject-elided passive report is not a question.
		"was rolled back after review, all good now",
	}
	for _, l := range reports {
		if _, ok := GiveUpLine(l); !ok {
			t.Errorf("a genuine report was dropped: %q", l)
		}
	}
}

func TestGiveUpRejectsParserProseAndTestSoundnessRollbacks(t *testing.T) {
	rejects := []string{
		// Algorithmic parser prose ("отбросил интервал") is not an engineering rollback.
		"Старый extractLineRanges отбросил интервал L10-L20 из-за пустой строки",
		// Temporary test-soundness and scratchpad rollbacks must not fire dead-end warnings.
		"Each new test failed with its fix reverted before passing.",
		"Verified by temporarily reverting the mcp.go hunk there: TestBlame fails.",
		"Against the reverted tree all four assertions fail.",
		"Test is sound. Reverting the fix fails TestRelayOutboxBatch.",
		"Fail-before confirmed on the reverted copy in /tmp/scratch.",
	}
	for _, s := range rejects {
		if got := GiveUpExcerpts(s); len(got) > 0 {
			t.Errorf("expected non-dead-end prose %q to be rejected, got %v", s, got)
		}
	}

	accepts := []string{
		"Отбросили этот вариант кеширования, потому что он ломает порядок событий.",
		"Отбросил подход с глобальным мьютексом из-за дедлока в воркере.",
	}
	for _, s := range accepts {
		if got := GiveUpExcerpts(s); len(got) == 0 {
			t.Errorf("expected genuine Russian dead-end %q to be accepted", s)
		}
	}
}

func TestGiveUpExcerptsExtractsTwoSentenceWindowFromLongParagraph(t *testing.T) {
	// Over giveUpLineMax (300 runes), with the target symbol in the opening
	// sentence and the rollback + causal reason in later sentences.
	longPara := "Confirmed: `blameTextResult` returns JSON via `mustMarshalBlame` because the protocol requires a structured array shape across all callers in the workspace and downstream clients depend on exact field names in every response payload. " +
		"Wrapping `blameTextResult` in markdown prose would break that contract for strict JSON consumers. " +
		"My framing attempt was wrong — reverted."
	got := GiveUpExcerpts(longPara)
	if len(got) == 0 {
		t.Fatalf("expected GiveUpExcerpts to extract a 2-sentence window from >300-rune paragraph")
	}
	joined := got[0]
	if _, ok := GiveUpLine(joined); !ok {
		t.Errorf("extracted window %q did not pass GiveUpLine", joined)
	}
	if len(got[0]) < 40 {
		t.Errorf("extracted window unexpectedly short: %q", joined)
	}
}

func gooseHeaderLine() string {
	b := mustJSON(map[string]any{
		"id": "g1", "description": "the ingest watermark work",
		"working_dir": "/work/app",
		"created_at":  "2026-08-30T09:00:00Z", "updated_at": "2026-08-30T09:00:00Z",
	})
	return string(b)
}

func gooseTurn(i int, text string) string {
	role := "user"
	if i%2 == 1 {
		role = "assistant"
	}
	b := mustJSON(map[string]any{"role": role, "content": text, "created": "2026-08-30T09:00:00Z"})
	return string(b)
}

// goose's JSONL sessions declare an offset parser the append gate never
// reached, so every pass re-read them whole (#2870). Adding them needs one
// thing first: goose writes the session's project in a header line, and an
// offset parse starts past it — measured before this, appending a turn moved
// the session from project "app" to the literal "goose".
func TestAGrowingGooseSessionKeepsItsProject(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	for k, v := range map[string]string{
		"DEJA_CLAUDE_ROOT": "none-claude", "DEJA_CODEX_ROOT": "none-codex",
		"DEJA_GOOSE_DB": "none-goose.db", "DEJA_OPENCODE_DB": "none-opencode.db",
		"DEJA_GROK_DB": "none-grok.db", "DEJA_HERMES_HOME": "none-hermes",
		"DEJA_ZED_DB": "none-zed.db", "DEJA_QWEN_ROOT": "none-qwen",
		"DEJA_NOTES_FILE": "notes.jsonl",
	} {
		t.Setenv(k, filepath.Join(tmp, v))
	}
	root := filepath.Join(tmp, "goose")
	t.Setenv("DEJA_GOOSE_ROOT", root)
	if err := os.MkdirAll(filepath.Join(root, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "sessions", "g1.jsonl")
	write := func(lines []string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seed := []string{gooseHeaderLine()} //nolint:prealloc // a fixture literal the loop below grows
	for i := range 6 {
		seed = append(seed, gooseTurn(i, fmt.Sprintf("seed %d zonkomatic", i)))
	}
	write(seed)

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	found := func(term string) []string {
		t.Helper()
		got, err := Search(dir, search.Options{Query: term, All: true})
		if err != nil {
			t.Fatal(err)
		}
		projects := make([]string, 0, len(got))
		for _, s := range got {
			projects = append(projects, s.Project)
		}
		return projects
	}
	hits := func(term string) int {
		t.Helper()
		got, err := Search(dir, search.Options{Query: term, All: true})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, s := range got {
			for _, m := range s.Messages {
				if strings.Contains(m.Text, term) {
					n++
				}
			}
		}
		return n
	}
	if p := found("zonkomatic"); len(p) != 1 || p[0] != "work/app" {
		t.Fatalf("the fixture did not index under its working_dir: %v", p)
	}

	write(append(append([]string{}, seed...), gooseTurn(6, "appended vantorquell")))
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if p := found("zonkomatic"); len(p) != 1 || p[0] != "work/app" {
		t.Errorf("appending a turn moved the session's project: %v", p)
	}
	if n := hits("appended vantorquell"); n != 1 {
		t.Errorf("the appended turn is not indexed: %d", n)
	}
	if n := hits("seed 0 "); n != 1 {
		t.Errorf("an earlier turn is held %d times", n)
	}

	// The rewind the prefix comparison exists for.
	rewritten := []string{gooseHeaderLine()} //nolint:prealloc // a fixture literal the loop below grows
	for i := range 9 {
		rewritten = append(rewritten, gooseTurn(i, fmt.Sprintf("rewritten %d zonkomatic", i)))
	}
	write(rewritten)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := hits("seed 0 "); n != 0 {
		t.Errorf("text from before the rewrite survived: %d", n)
	}
	if n := hits("rewritten 8 "); n != 1 {
		t.Errorf("the rewritten tail is not indexed: %d", n)
	}
}

// A goose session untouched since the watermark must survive an incremental
// pass triggered by a change to the shared sessions.db.
//
// The timestamps are written the way a real store writes them: sqlite's own
// format for updated_at, agreeing with the message timestamps to the second,
// because goose writes both from the same turn commit. The fixture used to put
// updated_at a week ahead of its messages and in RFC3339, a shape goose does not
// produce, and that gap decided what the since clause could be (#2032).
func TestGooseIncrementalKeepsUntouchedSessions(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}
	tmp := hermeticIndexEnv(t)
	t.Setenv("DEJA_GOOSE_ROOT", filepath.Join(tmp, "goose"))
	dir := filepath.Join(tmp, "index")
	db := filepath.Join(os.Getenv("DEJA_GOOSE_ROOT"), "sessions", "sessions.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := func(extra string) {
		schema := `create table if not exists sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table if not exists messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert or replace into sessions values ('20250724_old','old','Old chat','/w/app',datetime(1784278801,'unixepoch'),datetime(1784278802,'unixepoch'));
insert or replace into messages values (1,'20250724_old','user','[{"type":"text","text":"oldgoosefact about the pager"}]',1784278801);
insert or replace into messages values (2,'20250724_old','assistant','[{"type":"text","text":"oldgoose reply"}]',1784278802)` + extra + `;`
		if out, err := exec.CommandContext(t.Context(), "sqlite3", db, schema).CombinedOutput(); err != nil {
			t.Fatalf("seed: %v: %s", err, out)
		}
	}
	seed("")
	if err := Ensure(dir, "", false, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if hits, _ := Search(dir, search.Options{Query: "oldgoosefact", All: true}); len(hits) == 0 {
		t.Fatal("old goose session not indexed on first pass")
	}
	seed(`;
insert or replace into sessions values ('20250724_new','new','New chat','/w/app',datetime(1784282401,'unixepoch'),datetime(1784282402,'unixepoch'));
insert or replace into messages values (3,'20250724_new','user','[{"type":"text","text":"newgoosefact about caching"}]',1784282401);
insert or replace into messages values (4,'20250724_new','assistant','[{"type":"text","text":"newgoose reply"}]',1784282402)`)
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(db, future, future)
	if err := Ensure(dir, "", false, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if hits, _ := Search(dir, search.Options{Query: "newgoosefact", All: true}); len(hits) == 0 {
		t.Fatal("new goose session not indexed on incremental pass")
	}
	if hits, _ := Search(dir, search.Options{Query: "oldgoosefact", All: true}); len(hits) == 0 {
		t.Fatal("REGRESSION: untouched goose session vanished after incremental pass")
	}
	fmt.Fprintln(os.Stderr, "both goose sessions survived incremental")
}

// goose 1.10 moved its sessions into sessions.db and left the JSONL files
// behind, so the same conversation is in both stores under one id. That is a
// migration, not two transcripts clashing: the database is the live copy, and
// deja used to report every migrated session as an id collision and file the
// row under the superseded file, because the file name sorts first (#2066).
func TestAMigratedGooseSessionIsNotACollision(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none-opencode.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	root := filepath.Join(tmp, "goose")
	sessions := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_GOOSE_ROOT", root)
	db := filepath.Join(sessions, "sessions.db")
	t.Setenv("DEJA_GOOSE_DB", db)

	jsonl := `{"id":"20260101_120000","description":"pgbouncer pool","working_dir":"/tmp/app"}
{"role":"user","content":[{"type":"text","text":"why does pgbouncer time out"}],"created":1767268800}
`
	if err := os.WriteFile(filepath.Join(sessions, "20260101_120000.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}
	sql := `create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert into sessions values ('20260101_120000','n','pgbouncer pool','/tmp/app','2026-01-01 12:00:00','2026-01-01 12:05:00');
insert into messages values (1,'20260101_120000','user','[{"type":"text","text":"why does pgbouncer time out"}]',1767268800);
insert into messages values (2,'20260101_120000','user','[{"type":"text","text":"and after the restart"}]',1767269100);`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// The premise: both copies really did reach the same row, so the pair is
	// the one the fix is about.
	s, ok, err := FindByIdentity(dir, "goose", "20260101_120000")
	if err != nil || !ok {
		t.Fatalf("the session is not indexed at all, so this measures nothing: ok=%v err=%v", ok, err)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("the two stores did not merge into one session: %d messages", len(s.Messages))
	}

	if n := ReportCollisions(); n != 0 {
		t.Errorf("a migrated session was counted as %d id collision(s)", n)
	}
	if !strings.HasSuffix(s.Path, "sessions.db") {
		t.Errorf("the row is filed under %s, not the live store", s.Path)
	}
	if shared := HarnessSharedCounts(dir)["goose"]; shared != 0 {
		t.Errorf("doctor reports %d shared goose session(s)", shared)
	}
}

// A genuine clash between two transcripts must still be reported: two JSONL
// files under one id are two conversations, whatever the migration looks like.
func TestTwoGooseFilesUnderOneIDStillCollide(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none-opencode.db"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	root := filepath.Join(tmp, "goose")
	sessions := filepath.Join(root, "sessions", "archive")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_GOOSE_ROOT", root)
	for i, dirName := range []string{"a", "b"} {
		d := filepath.Join(sessions, dirName)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"id":"20260101_120000","description":"pgbouncer pool","working_dir":"/tmp/app` + dirName + `"}
{"role":"user","content":[{"type":"text","text":"conversation ` + dirName + `"}],"created":176726880` + string(rune('0'+i)) + `}
`
		if err := os.WriteFile(filepath.Join(d, "20260101_120000.jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if n := ReportCollisions(); n == 0 {
		t.Error("two transcripts sharing an id were not reported at all")
	}
}

// Goose writes its session header first and fills the description in later,
// once it has something to call the session. The reader takes the description
// from that header line, and an incremental pass starts past it — so what a
// listing shows depends on which build path touched the session last, the
// disagreement #R11 named for friction signatures (#2556).
func TestGooseDescriptionArrivesAfterTheFirstPass(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "goose")
	dir := filepath.Join(tmp, "index.db")
	sessions := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_GOOSE_ROOT", root)
	t.Setenv("DEJA_INDEX_DIR", dir)
	setHome(t, filepath.Join(tmp, "home"))
	path := filepath.Join(sessions, "g1.jsonl")

	header := func(desc string) string {
		return `{"id":"g1","working_dir":"/w/app","description":"` + desc +
			`","created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:00Z"}`
	}
	turn := func(role, text, at string) string {
		return `{"role":"` + role + `","created":"` + at + `","content":[{"type":"text","text":"` + text + `"}]}`
	}
	write := func(lines ...string) {
		body := ""
		var bodySb36 strings.Builder
		for _, l := range lines {
			bodySb36.WriteString(l + "\n")
		}
		body += bodySb36.String()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// First pass: goose has not named the session yet.
	write(header(""), turn("user", "the pool starves under load", "2026-08-01T10:00:00Z"))
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Sessions["goose:g1"].Title; got != "the pool starves under load" {
		t.Fatalf("precondition: first pass titled it %q", got)
	}

	// Goose names the session and the work goes on.
	write(header("pgbouncer pool sizing"),
		turn("user", "the pool starves under load", "2026-08-01T10:00:00Z"),
		turn("assistant", "raised pool_size to 40", "2026-08-01T10:05:00Z"))
	if err = Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err = readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	incremental := m.Sessions["goose:g1"].Title

	// What the same store says after a rebuild, which reads the header again.
	if err = Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m, err = readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt := m.Sessions["goose:g1"].Title
	if incremental != rebuilt {
		t.Errorf("the listing depends on which build path ran: incremental %q, rebuilt %q", incremental, rebuilt)
	}
	if rebuilt != "pgbouncer pool sizing" {
		t.Errorf("the name goose settled on is not what deja shows: %q", rebuilt)
	}
}

// One unreadable source file must not fail the whole index update: the file
// is skipped for the pass, its old records survive, and it is retried once
// readable again.
func TestUnreadableSourceSkippedNotFatal(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod 0 does not make files unreadable on windows or as root")
	}
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-grace")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(proj, "good.jsonl")
	bad := filepath.Join(proj, "bad.jsonl")
	line := func(sid, text string) string {
		return `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}
	if err := os.WriteFile(good, []byte(line("g1", "goodneedle stays")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte(line("b1", "badneedle original")), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	o := search.Options{Query: "badneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}

	// Rewrite bad.jsonl shorter (forces the non-append replace path) and make
	// it unreadable, then also shrink good.jsonl so the same pass has real work.
	if err := os.WriteFile(bad, []byte(line("b1", "badneedle updated")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bad, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(bad, 0o644) }()
	if err := os.WriteFile(good, []byte(line("g1", "goodneedle v2")), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureForSearch(dir, search.Options{Query: "goodneedle"}, false, nil); err != nil {
		t.Fatalf("update failed instead of skipping unreadable file: %v", err)
	}
	if ss, err := Search(dir, search.Options{Query: "goodneedle", All: true}); err != nil || len(ss) != 1 || ss[0].Messages[0].Text != "goodneedle v2" {
		t.Fatalf("good file not reindexed: %#v err=%v", ss, err)
	}
	// old records of the unreadable file survive
	if ss, err := Search(dir, search.Options{Query: "badneedle", All: true}); err != nil || len(ss) != 1 || ss[0].Messages[0].Text != "badneedle original" {
		t.Fatalf("old records lost for skipped file: %#v err=%v", ss, err)
	}

	// readable again → retried and updated
	if err := os.Chmod(bad, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureForSearch(dir, search.Options{Query: "badneedle"}, false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, err := Search(dir, search.Options{Query: "badneedle", All: true}); err != nil || len(ss) != 1 || ss[0].Messages[0].Text != "badneedle updated" {
		t.Fatalf("skipped file not retried: %#v err=%v", ss, err)
	}
}

func titleOf(ms ...model.Message) string {
	t, _ := sessionTitleFrom(model.Session{Messages: ms})
	return t
}

func userAt(text string, minute int) model.Message {
	return model.Message{Role: "user", Text: text, Time: time.Date(2026, 1, 2, 3, minute, 0, 0, time.UTC)}
}

// A session that opens with a greeting was named after it — 17 rows reading
// "hi" on a real 800-session store, with the turn that says what the work was
// one line below (#790).
func TestAGreetingDoesNotNameTheSession(t *testing.T) {
	for _, opener := range []string{"hi", "привет", "say ok", "hello again"} {
		got := titleOf(userAt(opener, 0), userAt("why does the docker build fail on arm64", 1))
		if got != "why does the docker build fail on arm64" {
			t.Errorf("opener %q: session titled %q", opener, got)
		}
	}
}

// A short instruction is not a greeting, and the length rule has to leave it
// alone: three words, or anything long enough to say what it wants.
func TestAShortInstructionKeepsItsTitle(t *testing.T) {
	for _, opener := range []string{"fix the build", "run the tests", "deploy staging"} {
		got := titleOf(userAt(opener, 0), userAt("and then the deploy to staging failed again", 1))
		if got != opener {
			t.Errorf("instruction %q was retitled to %q", opener, got)
		}
	}
}

// With nothing else to go on, the short turn still names the session: a title
// that says little beats no title at all.
func TestAGreetingAloneStillNamesTheSession(t *testing.T) {
	if got := titleOf(userAt("hi", 0)); got != "hi" {
		t.Errorf("a one-turn session lost its title: %q", got)
	}
	if got := titleOf(userAt("hi", 0), userAt("ok", 1)); got != "hi" {
		t.Errorf("two thin turns should keep the first: %q", got)
	}
}

// Ordered by the clock, like every other title path — a store titled locally
// and the same store imported elsewhere have to agree (#769).
func TestTheReplacementTitleFollowsTheClock(t *testing.T) {
	// The earlier turn sits later in the slice, so only the clock can pick it.
	got := titleOf(
		userAt("hi", 0),
		userAt("the second thing I asked", 5),
		userAt("the first thing I asked", 1),
	)
	if got != "the first thing I asked" {
		t.Errorf("the replacement did not follow the clock: %q", got)
	}
	// And the other way round, so neither slice order passes by luck.
	got = titleOf(
		userAt("hi", 0),
		userAt("the earlier thing I asked", 1),
		userAt("the later thing I asked", 5),
	)
	if got != "the earlier thing I asked" {
		t.Errorf("the replacement did not follow the clock: %q", got)
	}
	got = titleOf(
		userAt("hi", 0),
		userAt("the second thing I asked", 5),
		userAt("the first thing I asked", 1),
	)
	if got != "the first thing I asked" {
		t.Errorf("the replacement did not follow the clock: %q", got)
	}
}

// The format version is what makes existing rows re-derive their titles:
// incremental ingest reuses the row it already has, so the rules can change
// and nothing re-reads them (#784, and #790 for this change).
func TestTitleRulesRideOnTheFormatVersion(t *testing.T) {
	if version < 26 {
		t.Errorf("index version is %d — the title rules changed without the bump that re-derives them", version)
	}
}

// A live Grok session grows all day. Every touch used to reparse the whole
// updates.jsonl and rewrite the entire index — a gigabyte of records and
// buckets copied so a search could see one new line (#1522). Growth alone must
// take the append path now, while a rewind still rewrites: the prefix hash is
// what tells the two apart.
func TestGrokGrowthAppendsInsteadOfRewriting(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(tmp, "grok"))

	dir := filepath.Join(tmp, "grok", "sessions", url.PathEscape("/work/live"), "019f-grok-append")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	summary := filepath.Join(dir, "summary.json")
	write := func(path, body string, at time.Time) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	tick := time.Now()
	write(summary, `{"info":{"id":"019f-grok-append"},"generated_title":"Live run","created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:01Z"}`, tick)

	updates := filepath.Join(dir, "updates.jsonl")
	first := `{"timestamp":1785000001,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"appendfirstneedle opening question"},"_meta":{"promptIndex":0}}}}` + "\n"
	write(updates, first, tick)

	indexDir := filepath.Join(tmp, "index.db")
	if err := EnsureForSearch(indexDir, search.Options{Query: "appendfirstneedle", Harness: "grok"}, false, nil); err != nil {
		t.Fatal(err)
	}

	// One more ACP line, and a title bump alongside it — the two changes a live
	// session makes together.
	tick = tick.Add(10 * time.Second)
	second := `{"timestamp":1785000002,"params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"appendsecondneedle the answer"},"_meta":{}}}}` + "\n"
	write(updates, first+second, tick)
	write(summary, `{"info":{"id":"019f-grok-append"},"generated_title":"Live run continued","created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:09Z"}`, tick)

	var progress bytes.Buffer
	if err := EnsureForSearch(indexDir, search.Options{Query: "appendsecondneedle", Harness: "grok"}, false, &progress); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(progress.String(), "updated 1 file") {
		t.Errorf("growth did not take the append path:\n%s", progress.String())
	}
	for _, needle := range []string{"appendfirstneedle", "appendsecondneedle"} {
		ss, err := Search(indexDir, search.Options{Query: needle, Harness: "grok"})
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s: %#v err=%v", needle, ss, err)
		}
	}
	// Exactly the two turns: an append that re-reads from byte zero would
	// index the opening question a second time and search would still look
	// right.
	full, ok, err := FindByID(indexDir, "019f-grok-append")
	if err != nil || !ok {
		t.Fatalf("session missing after append: ok=%v err=%v", ok, err)
	}
	if len(full.Messages) != 2 {
		t.Errorf("%d messages after one appended line, want 2:\n%#v", len(full.Messages), full.Messages)
	}

	recent, err := Recent(indexDir, 1)
	if err != nil || len(recent) != 1 {
		t.Fatalf("recent: %#v err=%v", recent, err)
	}
	if recent[0].Title != "Live run continued" {
		t.Errorf("title = %q — the append path dropped the summary bump", recent[0].Title)
	}

	// A rewind truncates and regrows past the old size, which looks exactly
	// like an append until the prefix is compared. Appending onto it would
	// leave the replaced turn in the index for good.
	tick = tick.Add(10 * time.Second)
	rewound := `{"timestamp":1785000003,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"appendrewoundneedle a different opening, written out at length so the file regrows past every byte the previous two lines occupied together, which is what makes a rewind indistinguishable from growth until the prefix itself is compared"},"_meta":{"promptIndex":0}}}}` + "\n"
	if len(rewound) <= len(first+second) {
		t.Fatal("rewound fixture must regrow past the indexed size")
	}
	write(updates, rewound, tick)
	if err = EnsureForSearch(indexDir, search.Options{Query: "appendrewoundneedle", Harness: "grok"}, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(indexDir, search.Options{Query: "appendfirstneedle", Harness: "grok"})
	if err != nil || len(ss) != 0 {
		t.Fatalf("rewind kept the replaced turn: %#v err=%v", ss, err)
	}
	ss, err = Search(indexDir, search.Options{Query: "appendrewoundneedle", Harness: "grok"})
	if err != nil || len(ss) != 1 {
		t.Fatalf("rewind replacement missing: %#v err=%v", ss, err)
	}
}

// Grok Build writes the spawn edge into summary.json itself. deja treated every
// directory as an unrelated session, so after a hit on the parent there was no
// way to reach the child that did the work, and after a hit on the child no way
// to name what asked for it (#1385). Fixture fields are the ones the report
// quoted from a real store.
func TestGrokSpawnEdgeSurvivesTheIndex(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(tmp, "grok"))

	root := filepath.Join(tmp, "grok", "sessions", url.PathEscape("/work/spawn"))
	write := func(id, summary, text string) {
		t.Helper()
		dir := filepath.Join(root, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(summary), 0o644); err != nil {
			t.Fatal(err)
		}
		line := `{"timestamp":1785000001,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"` + text + `"},"_meta":{"promptIndex":0}}}}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, "updates.jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("01a00a65-parent", `{"info":{"id":"01a00a65-parent"},"generated_title":"Parent run","created_at":"2026-08-16T21:30:00Z","updated_at":"2026-08-16T21:30:01Z"}`, "spawnparentneedle the plan")
	write("01a00a65-child", `{"info":{"id":"01a00a65-child"},"generated_title":"Child run","created_at":"2026-08-16T21:31:19Z","updated_at":"2026-08-16T21:31:20Z","session_kind":"subagent_fork","parent_session_id":"01a00a65-parent","agent_name":"general-purpose","forked_at":"2026-08-16T21:31:19.371006700Z"}`, "spawnchildneedle the actual work")
	// A plain subagent records no parent. deja must keep it searchable and say
	// nothing about who spawned it.
	write("01a00a65-orphan", `{"info":{"id":"01a00a65-orphan"},"generated_title":"Sibling run","created_at":"2026-08-16T21:32:00Z","updated_at":"2026-08-16T21:32:01Z","session_kind":"subagent"}`, "spawnorphanneedle a sibling")

	dir := filepath.Join(tmp, "index.db")
	if err := EnsureForSearch(dir, search.Options{Query: "spawnchildneedle", Harness: "grok"}, false, nil); err != nil {
		t.Fatal(err)
	}

	child, ok, err := FindByID(dir, "01a00a65-child")
	if err != nil || !ok {
		t.Fatalf("child missing: ok=%v err=%v", ok, err)
	}
	if child.Parent != "01a00a65-parent" || child.Kind != "subagent_fork" || child.Agent != "general-purpose" {
		t.Errorf("spawn fields lost in the index: kind=%q parent=%q agent=%q", child.Kind, child.Parent, child.Agent)
	}

	kids, err := ChildrenOf(dir, "01a00a65-parent")
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 1 || kids[0].ID != "01a00a65-child" {
		t.Fatalf("children of the parent: %#v", kids)
	}

	orphan, ok, err := FindByID(dir, "01a00a65-orphan")
	if err != nil || !ok {
		t.Fatalf("orphan missing: ok=%v err=%v", ok, err)
	}
	if orphan.Kind != "subagent" {
		t.Errorf("kind = %q, want the harness's own word", orphan.Kind)
	}
	if orphan.Parent != "" {
		t.Errorf("parent = %q — deja invented an edge the harness did not record", orphan.Parent)
	}
	if kids, err := ChildrenOf(dir, "01a00a65-child"); err != nil || len(kids) != 0 {
		t.Errorf("a leaf reported children: %#v err=%v", kids, err)
	}
}

// seedGrokDB writes one session and its messages into a grok store, replacing
// what is there.
func seedGrokDB(t *testing.T, db string, msgs [][2]string) {
	t.Helper()
	stmts := `create table if not exists sessions (id text primary key, workspace_id text, title text, cwd_last text, created_at text);
create table if not exists messages (session_id text, seq integer, role text, message_json text, created_at text);
insert or replace into sessions values ('s1','w1','pool timeouts','/work/api','2026-01-01T10:00:00.000Z');
`
	var stmtsSb22 strings.Builder
	for i, m := range msgs {
		fmt.Fprintf(&stmtsSb22, "insert into messages values ('s1',%d,'user',json('{\"role\":\"user\",\"content\":\"%s\"}'),'%s');\n",
			i, m[0], m[1])
	}
	stmts += stmtsSb22.String()
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
}

// grok's database has a since-the-watermark parser wired into the registry and
// nothing ever stamped it, so its LastUpdated stayed 0 for the life of the
// index and every pass read the store whole — the pass with one new line to
// index cost what reading everything costs (#2075).
//
// The store is only safe to stamp because its parser selects messages rather
// than sessions and normalises both sides of the comparison with a millisecond
// backoff (#2150): the same shape as cursor. hermes and zed do
// neither, which is why they are not stamped here.
func TestTheGrokStoreIsAskedOnlyForWhatIsNew(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none-opencode.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "grok.db")
	t.Setenv("DEJA_GROK_DB", db)
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(tmp, "grok"))

	seedGrokDB(t, db, [][2]string{{"marker-grok-one", "2026-01-01T10:00:00.000Z"}})
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	hits := func(marker string) int {
		t.Helper()
		got, err := Search(dir, search.Options{Query: marker, All: true})
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	if hits("marker-grok-one") != 1 {
		t.Fatalf("the store was not indexed at all, so this measures nothing")
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Files[db].LastUpdated == 0 {
		t.Errorf("the store carries no watermark, so the next pass reads it whole")
	}

	// A line lands after the watermark, and one lands in the same millisecond
	// as it — the case the backoff exists for.
	seedGrokDB(t, db, [][2]string{
		{"marker-grok-same", "2026-01-01T10:00:00.000Z"},
		{"marker-grok-two", "2026-01-01T11:00:00.000Z"},
	})
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := hits("marker-grok-two"); n != 1 {
		t.Errorf("the line added after the watermark is not indexed: %d hits", n)
	}
	if n := hits("marker-grok-same"); n != 1 {
		t.Errorf("a line stamped at the watermark itself was skipped: %d hits", n)
	}
	// And the session is counted once: a cursor that hands back turns already
	// held would grow the count on every pass (#2025).
	if n := hits("marker-grok-one"); n != 1 {
		t.Errorf("the first line is held %d times", n)
	}
}

// The other half of stamping a shared store: the database changes whenever any
// session in it does, so a pass that reads only the new messages must not drop
// the records of the sessions it did not ask about — which is what the
// changed-file rule would do without fromDatabase knowing this store (#2033).
func TestAQuietGrokSessionSurvivesAPassThatDidNotAskForIt(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none-opencode.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "grok.db")
	t.Setenv("DEJA_GROK_DB", db)
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(tmp, "grok"))

	stmts := `create table sessions (id text primary key, workspace_id text, title text, cwd_last text, created_at text);
create table messages (session_id text, seq integer, role text, message_json text, created_at text);
insert into sessions values ('quiet','w1','quiet','/work/api','2026-01-01T09:00:00.000Z');
insert into sessions values ('busy','w1','busy','/work/api','2026-01-01T10:00:00.000Z');
insert into messages values ('quiet',0,'user',json('{"role":"user","content":"marker-grok-quiet"}'),'2026-01-01T09:00:00.000Z');
insert into messages values ('busy',0,'user',json('{"role":"user","content":"marker-grok-busy"}'),'2026-01-01T10:00:00.000Z');
`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	hits := func(marker string) int {
		t.Helper()
		got, err := Search(dir, search.Options{Query: marker, All: true})
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	if hits("marker-grok-quiet") != 1 || hits("marker-grok-busy") != 1 {
		t.Fatalf("both sessions were not indexed, so this measures nothing")
	}

	// Only the busy session gains a turn. The quiet one is not in what the
	// store hands back, and it has to still be there afterwards.
	add := `insert into messages values ('busy',1,'user',json('{"role":"user","content":"marker-grok-added"}'),'2026-01-01T11:00:00.000Z');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, add).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 add: %v %s", err, out)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := hits("marker-grok-added"); n != 1 {
		t.Errorf("the added turn is not indexed: %d hits", n)
	}
	if n := hits("marker-grok-quiet"); n != 1 {
		t.Errorf("the session the pass did not ask about was dropped: %d hits", n)
	}
}

// grok registers its database and its session files under one kind name, so
// "this pass read the store whole" cannot be decided by the kind. Deciding it
// that way marks the whole harness whole whenever a session file changes, and
// then the database's continued session — read from its watermark, so only the
// new turn came back — has its earlier turns dropped by key (#2075, the shape
// readWholeThisPass exists to prevent).
func TestATouchedGrokFileDoesNotCostTheDatabaseItsEarlierTurns(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none-opencode.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "grok.db")
	t.Setenv("DEJA_GROK_DB", db)
	root := filepath.Join(tmp, "grok")
	t.Setenv("DEJA_GROK_ROOT", root)

	// A session file beside the store, so both grok kinds are in this pass.
	fileDir := filepath.Join(root, "sessions", url.PathEscape("/work/app"), "019f-file-session")
	if err := os.MkdirAll(fileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	updates := filepath.Join(fileDir, "updates.jsonl")
	line := func(ts int, text string) string {
		return fmt.Sprintf(`{"timestamp":%d,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"%s"},"_meta":{"promptIndex":0}}}}`+"\n", ts, text)
	}
	if err := os.WriteFile(updates, []byte(line(1782900001, "marker-grok-file")), 0o644); err != nil {
		t.Fatal(err)
	}
	seedGrokDB(t, db, [][2]string{{"marker-grok-first", "2026-01-01T10:00:00.000Z"}})

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	hits := func(marker string) int {
		t.Helper()
		got, err := Search(dir, search.Options{Query: marker, All: true})
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	if hits("marker-grok-first") != 1 || hits("marker-grok-file") != 1 {
		t.Fatalf("the store and the session file were not both indexed, so this measures nothing")
	}

	// Both move in the same pass: the file gains a line, and the store's one
	// session gains a turn past its watermark.
	if err := os.WriteFile(updates,
		[]byte(line(1782900001, "marker-grok-file")+line(1782900009, "marker-grok-file-two")), 0o644); err != nil {
		t.Fatal(err)
	}
	add := `insert into messages values ('s1',9,'user',json('{"role":"user","content":"marker-grok-later"}'),'2026-01-01T12:00:00.000Z');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, add).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 add: %v %s", err, out)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := hits("marker-grok-later"); n != 1 {
		t.Errorf("the turn added to the store is not indexed: %d hits", n)
	}
	if n := hits("marker-grok-first"); n != 1 {
		t.Errorf("the store's earlier turn went with the touched session file: %d hits", n)
	}
}

func roleStore(t *testing.T, withTool bool) string {
	t.Helper()
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"type":"user","sessionId":"a","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"why does the build fail"}}` + "\n"
	if withTool {
		lines += `{"type":"user","sessionId":"a","timestamp":"2026-01-02T03:04:06Z","message":{"role":"user","content":[{"type":"tool_result","content":"go build ./... failed"}]}}` + "\n"
	}
	if err := os.WriteFile(filepath.Join(proj, "a.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// bigRoleStore is a store whose second record carries the tool role and whose
// remaining thousands do not, so asking for that role can stop at the front
// while asking for one nothing carries has to reach the end.
func bigRoleStore(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	t.Setenv("DEJA_INDEX_COMMANDS", "1")
	proj := filepath.Join(claudeRoot, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString(`{"type":"user","sessionId":"a","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"why does the build fail"}}` + "\n")
	b.WriteString(`{"type":"user","sessionId":"a","timestamp":"2026-01-02T03:04:06Z","message":{"role":"user","content":[{"type":"tool_result","content":"go build ./... failed"}]}}` + "\n")
	filler := strings.Repeat("the pgbouncer pool timed out and the retry took another second ", 12)
	for i := range 15000 {
		fmt.Fprintf(&b, `{"type":"assistant","sessionId":"a","timestamp":"2026-01-02T03:04:07Z","message":{"role":"assistant","content":%q}}`+"\n", fmt.Sprintf("%s %d", filler, i))
	}
	// One record of a second role at the very end: an implementation that gives
	// up on an absent role partway through the log answers "absent" for this
	// one, and a test that only measures cost would let it through.
	b.WriteString(`{"type":"user","sessionId":"a","timestamp":"2026-01-02T03:04:08Z","message":{"role":"user","content":[{"type":"tool_use","name":"Bash","input":{"command":"psql -c 'show pool'"}}]}}` + "\n")
	if err := os.WriteFile(filepath.Join(proj, "a.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The question behind "this index holds no tool records at all": absent means
// absent, present means present, and an index that cannot be read says nothing
// rather than claiming absence.
func TestHasRecordOfRole(t *testing.T) {
	if dir := roleStore(t, true); !HasRecordOfRole(dir, roleToolOutput) {
		t.Error("a store with a tool result reports none")
	}
	if dir := roleStore(t, false); HasRecordOfRole(dir, roleToolOutput) {
		t.Error("a talk-only store reports tool records")
	}
	if !HasRecordOfRole(filepath.Join(t.TempDir(), "no-index"), roleToolOutput) {
		t.Error("an unreadable index claimed the role is absent")
	}
}

// It stops at the first match rather than reading the whole log: the caller
// asks on every empty role search, and most stores that have the records have
// them near the front.
//
// Against a two-record store this measured nothing — fifty full scans of two
// records are nowhere near any wall-clock bound, so the case passed with the
// early exit deleted (#2003). A ratio against the scan that cannot stop early,
// over a store where a full pass costs something.
func TestHasRecordOfRoleStopsEarly(t *testing.T) {
	if testing.Short() {
		t.Skip("timing")
	}
	dir := bigRoleStore(t)

	// The role is in the second record of the log; the absent one is nowhere,
	// so answering it has to reach the end.
	// Best of three for the cheap side: a call costs ~120 microseconds, almost
	// all of it fixed, so one scheduler stall on a loaded runner is a multiple
	// of the whole measurement while the full scan absorbs the same stall.
	early := time.Hour
	for range 3 {
		started := time.Now()
		for range 20 {
			if !HasRecordOfRole(dir, roleToolOutput) {
				t.Fatal("lost the record it just found")
			}
		}
		if took := time.Since(started); took < early {
			early = took
		}
	}

	started := time.Now()
	for range 20 {
		if HasRecordOfRole(dir, "no-such-role") {
			t.Fatal("a role nothing carries was reported present")
		}
	}
	full := time.Since(started)

	// Cost is not correctness: an implementation that gives up on an absent
	// role partway through the log measures exactly like one that reads to the
	// end, and the last record is the one it would miss.
	if !HasRecordOfRole(dir, roleCommand) {
		t.Error("a role carried only by the last record was reported absent")
	}

	// The fixture has to make a full pass expensive, or the comparison below
	// compares two numbers that are both noise.
	if full < 20*time.Millisecond {
		t.Fatalf("a full scan of the fixture took %v for twenty passes, which is too cheap to measure against", full)
	}
	// Half the full scan. Deleting the early exit puts the ratio at 1.0, so
	// this still fails it by a factor of two, and it survives the contended
	// runs where a quarter did not.
	if early > full/2 {
		t.Errorf("finding the role in the second record took %v against %v to scan the whole log: it is not stopping early", early, full)
	}
	t.Logf("early %v, full %v", early, full)
}

// A bucket file cut short by a crash must read as "corrupt index" and
// SearchWithRecovery must rebuild and answer instead of erroring.
func TestSearchRecoversFromCorruptBucket(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-heal")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"type":"user","sessionId":"h1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"healneedle question"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "h1.jsonl"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	o := search.Options{Query: "healneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}

	// Truncate every bucket to simulate a crash mid-write.
	buckets, err := filepath.Glob(filepath.Join(dir, "buckets", "*.bin"))
	if err != nil || len(buckets) == 0 {
		t.Fatalf("buckets glob err=%v n=%d", err, len(buckets))
	}
	for _, b := range buckets {
		if err = os.Truncate(b, 3); err != nil {
			t.Fatal(err)
		}
	}

	if _, err = Search(dir, o); !IsCorrupt(err) {
		t.Fatalf("Search error = %v, want corrupt index", err)
	}
	ss, err := SearchWithRecovery(dir, o, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "h1" {
		t.Fatalf("recovery search bad result: %#v", ss)
	}
}

// The health map is how doctor answers "what could deja not read" — and the
// answer has to outlive an unrelated pass, because the lines it counts are
// still on disk. The merge path built a fresh manifest and carried five fields
// forward but not this one, so the whole map went at the next pass that took it
// (#2015).
func TestTheHealthMapSurvivesAnUnrelatedPass(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	proj := filepath.Join(tmp, "claude", "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	turn := func(id, ts, role, text string) string {
		return `{"type":"` + role + `","sessionId":"` + id + `","cwd":"/tmp/app","timestamp":"` + ts +
			`","message":{"role":"` + role + `","content":"` + text + `"}}` + "\n"
	}
	// The second line carries a raw escape, which makes it invalid JSON.
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(
		turn("s1", "2026-01-02T03:04:05Z", "user", "why does pgbouncer time out")+
			turn("s1", "2026-01-02T03:04:06Z", "user", "the pool \x1b[31mtimed out")), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The premise: the build that read the bad line recorded it. Without this
	// the assertions below would hold on a store that never lost anything.
	if got := m.IngestHealth["claude"].MalformedLines; got != 1 {
		t.Fatalf("the build recorded %d unreadable lines, so there is nothing to carry", got)
	}

	// A new session in the same store — a change that does not touch the file
	// holding the bad line. This is the merge path.
	if err = os.WriteFile(filepath.Join(proj, "s2.jsonl"), []byte(
		turn("s2", "2026-02-02T03:04:05Z", "user", "unrelated question about retries")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err = readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Sessions) != 2 {
		t.Fatalf("the pass indexed %d sessions, so it was not the merge path this is about", len(m.Sessions))
	}
	if got := m.IngestHealth["claude"].MalformedLines; got != 1 {
		t.Errorf("the unreadable line is still on disk and the count is %d: doctor has forgotten it", got)
	}

	// The other half: a file rewritten without its bad line clears its own
	// count, or carrying counts forward would make them permanent.
	if err = os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(
		turn("s1", "2026-01-02T03:04:05Z", "user", "why does pgbouncer time out")+
			turn("s1", "2026-01-02T03:04:06Z", "user", "the pool timed out")+
			turn("s1", "2026-01-02T03:04:07Z", "assistant", "raised it to 40")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err = readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.IngestHealth["claude"].MalformedLines; got != 0 {
		t.Errorf("the bad line is gone from the file and the count is still %d", got)
	}
}

// healthFixture is one claude store and the paths to build it with.
func healthFixture(t *testing.T) (dir, proj string, turn func(id, ts, role, text string) string) {
	t.Helper()
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	proj = filepath.Join(tmp, "claude", "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	turn = func(id, ts, role, text string) string {
		return `{"type":"` + role + `","sessionId":"` + id + `","cwd":"/tmp/app","timestamp":"` + ts +
			`","message":{"role":"` + role + `","content":"` + text + `"}}` + "\n"
	}
	return filepath.Join(tmp, "index.db"), proj, turn
}

func malformedFor(t *testing.T, dir, harness string) int { //nolint:unparam // the call names the harness it counts
	t.Helper()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	return m.IngestHealth[harness].MalformedLines
}

// The append path reads the tail, not the file, so what it finds adds to the
// file's count. Treating the file as re-read dropped every bad line already
// indexed — which is every live session, since a session that continues is
// exactly what this path is for.
func TestAppendingToASessionKeepsItsEarlierCount(t *testing.T) {
	dir, proj, turn := healthFixture(t)
	path := filepath.Join(proj, "s1.jsonl")
	if err := os.WriteFile(path, []byte(
		turn("s1", "2026-01-02T03:04:05Z", "user", "why does pgbouncer time out")+
			turn("s1", "2026-01-02T03:04:06Z", "user", "the pool \x1b[31mtimed out")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := malformedFor(t, dir, "claude"); got != 1 {
		t.Fatalf("the build recorded %d, so the appends below measure nothing", got)
	}

	grow := func(ts, text string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(turn("s1", ts, "assistant", text)); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	grow("2026-01-02T03:05:00Z", "raised the pool to 40")
	if got := malformedFor(t, dir, "claude"); got != 1 {
		t.Errorf("the session continued and the count went to %d, but the bad line is still in the file", got)
	}
	grow("2026-01-02T03:06:00Z", "and it \x1b[31mstill timed out")
	if got := malformedFor(t, dir, "claude"); got != 2 {
		t.Errorf("two bad lines in the file, count says %d", got)
	}
}

// A manifest written without a parse — Import writes one, and `deja sync` runs
// it in the same process right after an index pass — must not take the pass's
// files with it.
func TestAManifestWrittenWithoutAParseKeepsTheCounts(t *testing.T) {
	dir, proj, turn := healthFixture(t)
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(
		turn("s1", "2026-01-02T03:04:05Z", "user", "why does pgbouncer time out")+
			turn("s1", "2026-01-02T03:04:06Z", "user", "the pool \x1b[31mtimed out")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := malformedFor(t, dir, "claude"); got != 1 {
		t.Fatalf("the build recorded %d, so this measures nothing", got)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if got := malformedFor(t, dir, "claude"); got != 1 {
		t.Errorf("a manifest write that parsed nothing left the count at %d", got)
	}
}

// The clip count is this pass's — it is recorded during redaction, before the
// fold — so seeding the new manifest with the old total made every re-read of a
// clipped file add to it: one long message counted as two, then three.
func TestAClippedMessageIsCountedOncePerPass(t *testing.T) {
	dir, proj, turn := healthFixture(t)
	path := filepath.Join(proj, "s1.jsonl")
	long := strings.Repeat("pgbouncer pool timed out and the retry took a second ", 1600)
	if len(long) < maxIndexedText {
		t.Fatalf("the fixture message is %d bytes, under the %d that gets it clipped", len(long), maxIndexedText)
	}
	if err := os.WriteFile(path, []byte(turn("s1", "2026-01-02T03:04:05Z", "user", long)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	clipped := func() int {
		t.Helper()
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		return m.IngestHealth["claude"].ClippedMessages
	}
	if got := clipped(); got != 1 {
		t.Fatalf("the build clipped %d messages, so this measures nothing", got)
	}
	for i, tail := range []string{"first answer", "second answer", "third answer"} {
		if err := os.WriteFile(path, []byte(
			turn("s1", "2026-01-02T03:04:05Z", "user", long)+
				turn("s1", "2026-01-02T03:04:06Z", "assistant", tail)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		if got := clipped(); got != 1 {
			t.Fatalf("one long message in the file, rewrite %d says %d clipped", i+1, got)
		}
	}
}

// A file that failed to open once and then reads fine has to stop being
// reported: transcripts are append-only, so the append path is the one that
// reads it again, and it is the path that cannot clear an entry by re-reading.
// Left alone, a permission blip stayed in doctor until a forced rebuild.
func TestAFileThatOpensAgainStopsBeingReportedUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not stop a read here")
	}
	dir, proj, turn := healthFixture(t)
	path := filepath.Join(proj, "s1.jsonl")
	if err := os.WriteFile(path, []byte(turn("s1", "2026-01-02T03:04:05Z", "user", "why does pgbouncer time out")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	failed := func() int {
		t.Helper()
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		return m.IngestHealth["claude"].FailedFiles
	}
	if got := failed(); got != 1 {
		t.Fatalf("the unreadable file was reported %d times, so this measures nothing", got)
	}

	// The blip passes and the session carries on, which is an append.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(turn("s1", "2026-01-02T03:05:00Z", "assistant", "raised the pool to 40")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := failed(); got != 0 {
		t.Errorf("deja read the file and still reports %d unreadable", got)
	}
}

func clippedFor(t *testing.T, dir, harness string) int { //nolint:unparam // the call names the harness it counts
	t.Helper()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	return m.IngestHealth[harness].ClippedMessages
}

// Clips were the last ingest number kept per harness, so a pass over some other
// file reset them to what it found — nothing — while the long message sat where
// it always had (#2022).
func TestAClipSurvivesAPassOverAnotherFile(t *testing.T) {
	dir, proj, turn := healthFixture(t)
	long := strings.Repeat("pgbouncer pool timed out and the retry took a second ", 1600)
	if len(long) < maxIndexedText {
		t.Fatalf("the fixture message is %d bytes, under the %d that gets it clipped", len(long), maxIndexedText)
	}
	a := filepath.Join(proj, "a.jsonl")
	b := filepath.Join(proj, "b.jsonl")
	if err := os.WriteFile(a, []byte(turn("a", "2026-01-02T03:04:05Z", "user", long)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte(turn("b", "2026-01-02T03:04:05Z", "user", "a short question")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := clippedFor(t, dir, "claude"); got != 1 {
		t.Fatalf("the build clipped %d messages, so this measures nothing", got)
	}

	// b rewritten shorter. A rewrite that only grows is still an append, and
	// the append path keeps the old manifest — this is the path that does not.
	if err := os.WriteFile(b, []byte(turn("b", "2026-01-02T03:04:05Z", "user", "short")), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := Ensure(dir, "", false, &out); err != nil {
		t.Fatal(err)
	}
	if said := out.String(); !strings.Contains(said, replacementPassMarker) {
		t.Fatalf("this was not the merge path, so it does not measure what it is about: %q", said)
	}
	if got := clippedFor(t, dir, "claude"); got != 1 {
		t.Errorf("the long message is still in the other file and the count is %d", got)
	}

	// And the file that holds it, re-read by the merge path: counted once, not
	// once more. Shorter than it was so the pass cannot append, still long
	// enough to be clipped.
	shorter := long[:len(long)-1000]
	if len(shorter) < maxIndexedText {
		t.Fatalf("the re-read fixture is %d bytes, under the %d that gets it clipped", len(shorter), maxIndexedText)
	}
	if err := os.WriteFile(a, []byte(turn("a", "2026-01-02T03:04:05Z", "user", shorter)), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Ensure(dir, "", false, &out); err != nil {
		t.Fatal(err)
	}
	if said := out.String(); !strings.Contains(said, replacementPassMarker) {
		t.Fatalf("the re-read did not take the merge path: %q", said)
	}
	if got := clippedFor(t, dir, "claude"); got != 1 {
		t.Errorf("one long message, count says %d", got)
	}
}

// A pass that could not read a file keeps what that file already held: it is
// the one pass with nothing to say about it. The merge branch decides which
// files it re-read after the parse loop for this reason — the loop drops the
// ones it failed on.
func TestAFileThePassCouldNotReadKeepsItsCounts(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not stop a read here")
	}
	dir, proj, turn := healthFixture(t)
	long := strings.Repeat("pgbouncer pool timed out and the retry took a second ", 1600)
	a := filepath.Join(proj, "a.jsonl")
	if err := os.WriteFile(a, []byte(turn("a", "2026-01-02T03:04:05Z", "user", long)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := clippedFor(t, dir, "claude"); got != 1 {
		t.Fatalf("the build clipped %d messages, so this measures nothing", got)
	}

	// The file changes and becomes unreadable in the same breath, which is what
	// a store being rewritten under a lock looks like.
	if err := os.WriteFile(a, []byte(turn("a", "2026-01-02T03:04:05Z", "user", long+" and more")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(a, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(a, 0o644) })
	var out strings.Builder
	if err := Ensure(dir, "", false, &out); err != nil {
		t.Fatal(err)
	}
	if said := out.String(); !strings.Contains(said, "skipping") {
		t.Fatalf("the pass read the file after all, so it does not measure what it is about: %q", said)
	}
	if got := clippedFor(t, dir, "claude"); got != 1 {
		t.Errorf("the pass could not read the file and dropped its count anyway: %d", got)
	}
}

// The run narrates a store and doctor filed the same fact under the file kind —
// "cline-sdk" where the screen said "cline" — while the documentation calls the
// key a harness. A reader carrying a number from one to the other had to know a
// mapping deja never showed them (#2234).
func TestIngestHealthIsKeyedByTheStoreTheRunNames(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	cline := filepath.Join(tmp, "cline")
	t.Setenv("DEJA_CLINE_ROOT", cline)

	proj := filepath.Join(claude, "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	good := fmt.Sprintf(`{"type":"user","sessionId":"s1","timestamp":%q,"cwd":"/work/app",`+
		`"message":{"role":"user","content":"the pool timed out during the migration"}}`, stamp)
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(good+"\nnot json at all\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cline, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cline, "1234.messages.json"), []byte(`[{"ts":1,`), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(tmp, "index.db")
	var out strings.Builder
	if err := Ensure(dir, "", true, &out); err != nil {
		t.Fatal(err)
	}
	health := IngestHealth(dir)
	// The premise: both stores are in there, or the names below prove nothing.
	if len(health) < 2 {
		t.Fatalf("only %d store reported anything:\n%s\n%v", len(health), out.String(), health)
	}
	for name := range health {
		if sources.HarnessForKind(name) != name {
			t.Errorf("doctor files this under %q, which is a file kind; the run says %q",
				name, sources.HarnessForKind(name))
		}
	}
	// And the store the run named is findable by that name.
	if _, ok := health["cline"]; !ok {
		t.Errorf("nothing is filed under the name the run printed: %v", health)
	}
}

// A Postgres-backed Hermes has no state.db to stat, so it went dark: the whole
// harness was invisible while doctor called the store healthy. It is indexed
// from a query fingerprint now, and picks up new rows incrementally (#1018).
func TestHermesPostgresIsIndexedAndIncremental(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	t.Setenv("DEJA_HERMES_PG_DSN", "postgres://fake/hermes")

	// A tiny fake store: two rows, growing to three between reads.
	rows := []string{ //nolint:prealloc // a fixture literal the loop below grows
		`{"session_id":"h1","role":"user","content":"we chose bounded channels for backpressure","timestamp":1785000000}`,
		`{"session_id":"h1","role":"assistant","content":"pgx was pinned instead","timestamp":1785000060}`,
	}
	fake := func(_, sql string) ([]byte, error) {
		if strings.HasPrefix(sql, "select count(*)") {
			return []byte(fmt.Sprintf("%d|%d\n", len(rows), 1785000000+60*(len(rows)-1))), nil
		}
		return []byte("[" + strings.Join(rows, ",") + "]"), nil
	}
	prev := sources.HermesPGRunner
	sources.HermesPGRunner = fake
	defer func() { sources.HermesPGRunner = prev }()

	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	hits, err := Search(dir, query.Options{Query: "backpressure"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("a postgres-backed hermes session was not indexed")
	}
	if hits[0].Harness != "hermes" {
		t.Fatalf("harness = %q", hits[0].Harness)
	}

	// A new row lands; an incremental pass must find it without a forced rebuild.
	rows = append(rows, `{"session_id":"h2","role":"user","content":"the retry queue overflowed under load","timestamp":1785000120}`)
	if err = Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	hits, err = Search(dir, query.Options{Query: "retry queue overflowed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("the incremental pass missed the new postgres row")
	}
}

// An unset DSN is an env that was turned off for one run, not a deletion: the
// sessions already indexed must survive it, the way an unmounted disk's do.
func TestHermesPostgresSurvivesADisabledDSN(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	t.Setenv("DEJA_HERMES_PG_DSN", "postgres://fake/hermes")

	fake := func(_, sql string) ([]byte, error) {
		if strings.HasPrefix(sql, "select count(*)") {
			return []byte("1|1785000000\n"), nil
		}
		return []byte(`[{"session_id":"h1","role":"user","content":"a decision about queue backpressure","timestamp":1785000000}]`), nil
	}
	prev := sources.HermesPGRunner
	sources.HermesPGRunner = fake
	if err := Ensure(dir, "", true, nil); err != nil {
		sources.HermesPGRunner = prev
		t.Fatal(err)
	}
	sources.HermesPGRunner = prev

	// DSN gone this run. The store must not be tombstoned out of the index.
	t.Setenv("DEJA_HERMES_PG_DSN", "")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	hits, err := Search(dir, query.Options{Query: "backpressure"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("a disabled DSN dropped the postgres sessions as if forgotten")
	}
}

// Hermes titled its sessions in the parser, so the index's greeting rule
// (#790) never ran for it: a session opened with "hi" listed as "hi", with the
// question one line below. The parser leaves the title to the index now, like
// the other parsers do (#3251).
func TestHermesGreetingDoesNotNameTheSession(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is not installed")
	}
	dir := filepath.Join(t.TempDir(), "writer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "state.db")
	cmd := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd.Stdin = strings.NewReader(`CREATE TABLE messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		role TEXT NOT NULL,
		content TEXT,
		timestamp REAL NOT NULL);
		INSERT INTO messages (session_id,role,content,timestamp) VALUES
		 ('hi','user','hi',1785234496.0),
		 ('hi','assistant','Hi! What can I help with?',1785234500.0),
		 ('hi','user','hiddify configs: the widgetd import',1785234520.0),
		 ('greet','assistant','hello, what shall we do about the ptarmigan cache',1785000000.0),
		 ('greet','user','why does the ptarmigan cache miss on restart',1785000010.0),
		 ('bot','assistant','nobody typed here',1785100000.0);`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v\n%s", err, out)
	}
	ss, err := sources.ParseHermesDB(db)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := map[string]string{
		"hi":    "hiddify configs: the widgetd import",
		"greet": "why does the ptarmigan cache miss on restart",
		"bot":   "nobody typed here",
	}
	for _, s := range ss {
		if got := metaForSession(s).Title; got != want[s.ID] {
			t.Errorf("session %s titled %q, want %q", s.ID, got, want[s.ID])
		}
	}
}

// hermesEnv points every other store somewhere empty and returns the hermes
// home, so a pass sees this store and nothing else.
func hermesEnv(t *testing.T) (tmp, home string) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp = t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none-opencode.db"))
	t.Setenv("DEJA_GROK_DB", filepath.Join(tmp, "none-grok.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	home = filepath.Join(tmp, "hermes")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_HERMES_HOME", home)
	return tmp, home
}

func seedHermes(t *testing.T, db string, rows string) {
	t.Helper()
	stmts := `create table if not exists messages (id integer primary key, session_id text, role text, content text, timestamp real);` + rows
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
}

func hermesHits(t *testing.T, dir, marker string) int {
	t.Helper()
	got, err := Search(dir, search.Options{Query: marker, All: true})
	if err != nil {
		t.Fatal(err)
	}
	return len(got)
}

// hermes had a since-the-watermark parser wired into the registry and nothing
// stamped its store, so every pass read the whole history to index one line
// (#2075). Stamping it needs the second-resolution backoff first: without it
// the turns sharing the watermark's second are skipped for good.
func TestTheHermesStoreIsAskedOnlyForWhatIsNew(t *testing.T) {
	tmp, home := hermesEnv(t)
	db := filepath.Join(home, "state.db")
	seedHermes(t, db, `insert into messages values (1,'s1','user','marker-hermes-one',1767268800);`)

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if hermesHits(t, dir, "marker-hermes-one") != 1 {
		t.Fatalf("the store was not indexed at all, so this measures nothing")
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Files[db].LastUpdated == 0 {
		t.Errorf("the store carries no watermark, so the next pass reads it whole")
	}

	// One turn after the watermark, and one in the same whole second as it.
	seedHermes(t, db, `insert into messages values (2,'s1','user','marker-hermes-same',1767268800);`+
		`insert into messages values (3,'s1','user','marker-hermes-two',1767272400);`)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := hermesHits(t, dir, "marker-hermes-two"); n != 1 {
		t.Errorf("the turn added after the watermark is not indexed: %d hits", n)
	}
	if n := hermesHits(t, dir, "marker-hermes-same"); n != 1 {
		t.Errorf("a turn stamped in the watermark's own second was skipped: %d hits", n)
	}
	if n := hermesHits(t, dir, "marker-hermes-one"); n != 1 {
		t.Errorf("the first turn is held %d times", n)
	}
}

// The other half: the store changes whenever any session in it does, so a pass
// that reads only the new messages must not drop the sessions it did not ask
// about. Without fromDatabase knowing the store, the changed-file rule takes
// them — which is what stamping turns on.
func TestAQuietHermesSessionSurvivesAPassThatDidNotAskForIt(t *testing.T) {
	tmp, home := hermesEnv(t)
	db := filepath.Join(home, "state.db")
	seedHermes(t, db, `insert into messages values (1,'quiet','user','marker-hermes-quiet',1767268800);`+
		`insert into messages values (2,'busy','user','marker-hermes-busy',1767272400);`)

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if hermesHits(t, dir, "marker-hermes-quiet") != 1 || hermesHits(t, dir, "marker-hermes-busy") != 1 {
		t.Fatalf("both sessions were not indexed, so this measures nothing")
	}

	seedHermes(t, db, `insert into messages values (3,'busy','user','marker-hermes-added',1767276000);`)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := hermesHits(t, dir, "marker-hermes-added"); n != 1 {
		t.Errorf("the added turn is not indexed: %d hits", n)
	}
	if n := hermesHits(t, dir, "marker-hermes-quiet"); n != 1 {
		t.Errorf("the session the pass did not ask about was dropped: %d hits", n)
	}
}

// Older builds shard the store per profile, so a machine can hold several. Each
// carries its own watermark: stamping them alike would give a quiet profile the
// busy one's time and skip everything it gained below that line (#2071).
func TestEachHermesProfileKeepsItsOwnWatermark(t *testing.T) {
	tmp, home := hermesEnv(t)
	profiles := filepath.Join(home, "profiles")
	dbs := make([]string, 0, 2)
	for i, name := range []string{"busy", "quiet"} {
		dir := filepath.Join(profiles, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		db := filepath.Join(dir, "state.db")
		seedHermes(t, db, fmt.Sprintf(`insert into messages values (1,'s%d','user','marker-hermes-%s',%d);`,
			i, name, 1767268800+int64(i)*-3600))
		dbs = append(dbs, db)
	}

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if hermesHits(t, dir, "marker-hermes-busy") != 1 || hermesHits(t, dir, "marker-hermes-quiet") != 1 {
		t.Fatalf("the two profiles were not both indexed, so this measures nothing")
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Files[dbs[1]].LastUpdated == m.Files[dbs[0]].LastUpdated {
		t.Errorf("both profiles carry one watermark (%d), so the quiet one is stamped with the busy one's newest",
			m.Files[dbs[1]].LastUpdated)
	}

	// The quiet profile gains a turn newer than anything it holds and older
	// than the busy profile's newest.
	seedHermes(t, dbs[1], `insert into messages values (2,'s1','user','marker-hermes-quiet-two',1767267000);`)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := hermesHits(t, dir, "marker-hermes-quiet-two"); n != 1 {
		t.Errorf("the turn added to the quiet profile is not indexed: %d hits", n)
	}
}

// A build against a path that holds a file used to delete the file: the swap
// parks whatever is at the index path as <dir>.old, renames the new index into
// place, and then removes the parked copy. Exit code 0, nothing printed, and a
// file somebody put there gone — DEJA_INDEX_DIR pointing at a database by
// mistake is enough to reach it.
func TestABuildRefusesAnIndexPathThatIsAFile(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "index.db")
	const content = "notes somebody kept here\n"
	if err := os.WriteFile(dir, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Ensure(dir, "", true, nil)
	if err == nil {
		t.Fatal("a build against a file for an index path reported success")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("the refusal does not name the path: %v", err)
	}
	b, rerr := os.ReadFile(dir)
	if rerr != nil {
		t.Fatalf("the file at the index path is gone: %v", rerr)
	}
	if string(b) != content {
		t.Errorf("the file at the index path was rewritten: %q", b)
	}
}
