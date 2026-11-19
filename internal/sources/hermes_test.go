package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// /branch copies a conversation into a new session and marks it with the one
// it came from in model_config._branched_from. Read as an unrelated session,
// the branch's recall answered with its source (#5141). A compression's
// continuation also names a parent, without the marker, and stays as it is.
func TestAHermesBranchIsAForkOfItsSource(t *testing.T) {
	db := writeHermesDB(t, filepath.Join(t.TempDir(), "p"), `
		CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT, parent_session_id TEXT, cwd TEXT, model_config TEXT);
		INSERT INTO sessions VALUES
		 ('20261018_012943_14524c','cli',NULL,'/w/app','{"max_iterations":90}'),
		 ('20261018_013350_8f1cbe','cli','20261018_012943_14524c',NULL,'{"_branched_from":"20261018_012943_14524c"}'),
		 ('20261018_014000_aaaaaa','cli','20261018_012943_14524c','/w/app','{"max_iterations":90}'),
		 ('20261018_015000_bbbbbb','cli',NULL,'/w/app','not json');
		INSERT INTO messages (session_id,role,content,timestamp) VALUES
		 ('20261018_012943_14524c','user','password hashing uses bcrypt cost 12',1792200000),
		 ('20261018_013350_8f1cbe','user','password hashing uses bcrypt cost 12',1792200100),
		 ('20261018_014000_aaaaaa','user','summary of the earlier turns',1792200200),
		 ('20261018_015000_bbbbbb','user','a row whose model_config is not json',1792200300);`)
	ss, err := ParseHermesDB(db)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range ss {
		got[s.ID] = s.Kind + "/" + s.Parent + "/" + s.Project
	}
	want := map[string]string{
		"20261018_012943_14524c": "//w/app",
		"20261018_013350_8f1cbe": "fork/20261018_012943_14524c/w/app",
		"20261018_014000_aaaaaa": "//w/app",
		"20261018_015000_bbbbbb": "//w/app",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s = %q, want %q", id, got[id], w)
		}
	}

	// Read off the store by id, for a branch no build has seen.
	t.Setenv("DEJA_HERMES_DB", db)
	h, ok := HermesSessionHead("20261018_013350_8f1cbe")
	if !ok || h.Kind != "fork" || h.Parent != "20261018_012943_14524c" || h.Harness != "hermes" {
		t.Errorf("head of the branch = %+v %v", h, ok)
	}
	if _, ok := HermesSessionHead("20261018_999999_cccccc"); ok {
		t.Error("an id no store holds was found")
	}
	if _, ok := HermesSessionHead("x' or '1'='1"); ok {
		t.Error("an id that is not Hermes' shape reached the query")
	}
}

// Hermes finds its own home through HERMES_HOME; deja read only its own
// override, so a Hermes moved by its own switch was invisible to install,
// parse and doctor (#3203).
func TestHermesHomeFollowsHermesOwnSwitch(t *testing.T) {
	t.Setenv("DEJA_HERMES_HOME", "")
	t.Setenv("HERMES_HOME", filepath.Join(t.TempDir(), "hermes-alt"))
	if got, want := HermesHome(), filepath.Join(t.TempDir(), "hermes-alt"); filepath.Base(got) != filepath.Base(want) {
		t.Fatalf("HermesHome = %q, want the HERMES_HOME dir", got)
	}
	// deja's own override still wins.
	t.Setenv("DEJA_HERMES_HOME", filepath.Join(t.TempDir(), "deja-says"))
	if got := HermesHome(); filepath.Base(got) != "deja-says" {
		t.Fatalf("HermesHome = %q, want DEJA_HERMES_HOME to win", got)
	}
}

// Hermes records who started a session in sessions.source and the spawning
// session in parent_session_id. A subagent's run folds under its parent and a
// cron job's run is marked as one (#4948); a store from before those columns
// still reads its cwd.
func TestHermesSessionKindsComeFromTheSessionsTable(t *testing.T) {
	root := t.TempDir()
	db := writeHermesDB(t, filepath.Join(root, "p"), `
		CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT, parent_session_id TEXT, cwd TEXT);
		INSERT INTO sessions VALUES ('s-cli','cli',NULL,'/w/app'),('s-sub','subagent','s-cli','/w/app'),('s-cron','cron',NULL,'/w/app'),('s-orphan','subagent',NULL,NULL);
		INSERT INTO messages (session_id,role,content,timestamp) VALUES
		 ('s-cli','user','plan the migration',1785000000),
		 ('s-sub','user','read the schema',1785000010),
		 ('s-cron','user','nightly digest',1785000020),
		 ('s-orphan','user','a child whose parent is not recorded',1785000030);`)
	ss, err := ParseHermesDB(db)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range ss {
		got[s.ID] = s.Kind + "/" + s.Parent + "/" + s.Project
	}
	want := map[string]string{
		"s-cli":    "//w/app",
		"s-sub":    "subagent/s-cli/w/app",
		"s-cron":   "cron//w/app",
		"s-orphan": "//p",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s = %q, want %q", id, got[id], w)
		}
	}

	old := writeHermesDB(t, filepath.Join(root, "old"), `
		CREATE TABLE sessions (id TEXT PRIMARY KEY, cwd TEXT);
		INSERT INTO sessions VALUES ('s-1','/w/app');
		INSERT INTO messages (session_id,role,content,timestamp) VALUES ('s-1','user','plan the migration',1785000000);`)
	ss, err = ParseHermesDB(old)
	if err != nil || len(ss) != 1 || ss[0].Project != "w/app" || ss[0].Kind != "" {
		t.Fatalf("a store without source: %+v %v", ss, err)
	}
}

// On Postgres a /branch copy and a delegate_task child came out as sessions of
// their own in project "hermes": the read took cwd, source and parent only, not
// model_config's _branched_from / _delegate_from that the SQLite read uses
// (#5294). A row whose model_config is not JSON keeps everything else.
func TestParseHermesPGReadsBranchAndDelegateMarks(t *testing.T) {
	f := &fakePG{
		rows: `[
		{"session_id":"s-src","role":"user","content":"plan the migration","timestamp":1785000000},
		{"session_id":"s-br","role":"user","content":"try it the other way","timestamp":1785000010},
		{"session_id":"s-del","role":"user","content":"read the schema","timestamp":1785000020},
		{"session_id":"s-odd","role":"user","content":"anything","timestamp":1785000030}
	]`,
		columns: `["id","source","parent_session_id","cwd","model_config"]`,
		sessions: `[{"id":"s-src","source":"tui","cwd":"/w/app","model_config":"{\"max_iterations\":90}"},
		{"id":"s-br","source":"tui","model_config":"{\"_branched_from\":\"s-src\"}"},
		{"id":"s-del","source":"tui","model_config":"{\"_delegate_from\":\"s-src\"}"},
		{"id":"s-odd","source":"cli","cwd":"/w/lib","model_config":"not json"}]`,
	}
	defer SetHermesPGRunner(f.run)()
	ss, err := ParseHermesPG("postgres://deja@192.0.2.1/hermes", 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range ss {
		got[s.ID] = s.Kind + "/" + s.Parent + "/" + s.Project
	}
	want := map[string]string{
		"s-src": "//w/app",
		"s-br":  "fork/s-src/w/app",
		"s-del": "subagent/s-src/w/app",
		"s-odd": "//w/lib",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s = %q, want %q", id, got[id], w)
		}
	}
}

func TestHermesModelConfigMarks(t *testing.T) {
	tests := []struct {
		config, branched, delegated string
	}{
		{`{"_branched_from":"a"}`, "a", ""},
		{`{"_delegate_from":" b "}`, "", "b"},
		{`{"_branched_from":"a","_delegate_from":"b"}`, "a", "b"},
		{`{"_branched_from":7}`, "", ""},
		{`{}`, "", ""},
		{``, "", ""},
		{`null`, "", ""},
		{`[1,2]`, "", ""},
		{`not json`, "", ""},
	}
	for _, tc := range tests {
		b, d := hermesModelConfigMarks(tc.config)
		if b != tc.branched || d != tc.delegated {
			t.Errorf("hermesModelConfigMarks(%q) = %q, %q; want %q, %q", tc.config, b, d, tc.branched, tc.delegated)
		}
	}
}

// fakePG stands in for psql: it answers the fingerprint query from counts and
// the json_agg query from a canned array, and records the SQL it saw.
type fakePG struct {
	fingerprint string
	rows        string
	// columns and sessions answer the sessions-table reads; empty, the store
	// has no sessions table.
	columns, sessions string
	lastSQL           string
	err               error
}

func (f *fakePG) run(_, sql string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	switch {
	case strings.Contains(sql, "information_schema.columns"):
		return []byte(f.columns + "\n"), nil
	case strings.Contains(sql, "from sessions"):
		return []byte(f.sessions + "\n"), nil
	}
	f.lastSQL = sql
	if strings.HasPrefix(sql, "select count(*)") {
		return []byte(f.fingerprint + "\n"), nil
	}
	return []byte(f.rows), nil
}

func SetHermesPGRunner(f func(dsn, sql string) ([]byte, error)) func() {
	prev := HermesPGRunner
	HermesPGRunner = f
	return func() { HermesPGRunner = prev }
}

func TestParseHermesPGReadsRowsLikeSQLite(t *testing.T) {
	f := &fakePG{rows: `[
		{"session_id":"s1","role":"user","content":"switch the pool to transaction mode","timestamp":1785000000},
		{"session_id":"s1","role":"assistant","content":"pgx was pinned instead","timestamp":1785000060},
		{"session_id":"s2","role":"user","content":"","timestamp":1785000120}
	]`}
	defer SetHermesPGRunner(f.run)()

	ss, err := ParseHermesPG("postgres://x", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("want 1 session (s2 has only empty content), got %d", len(ss))
	}
	if ss[0].ID != "s1" || len(ss[0].Messages) != 2 {
		t.Fatalf("session = %+v", ss[0])
	}
	if ss[0].Project != "hermes" || ss[0].Harness != "hermes" {
		t.Fatalf("project/harness = %q/%q", ss[0].Project, ss[0].Harness)
	}
	// The title is the index's to derive (#3251).
	if ss[0].Title != "" {
		t.Fatalf("title = %q, want none", ss[0].Title)
	}
}

func TestParseHermesPGSinceFiltersByTimestamp(t *testing.T) {
	f := &fakePG{rows: `[]`}
	defer SetHermesPGRunner(f.run)()

	if _, err := ParseHermesPG("postgres://x", 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.lastSQL, "timestamp >") {
		t.Errorf("a full read carried a since clause: %s", f.lastSQL)
	}
	// 1785000000 unix is the cutoff; the query must ask only for newer rows.
	if _, err := ParseHermesPG("postgres://x", 1785000000*1_000_000_000); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.lastSQL, "timestamp > 1785000000") {
		t.Errorf("since clause missing: %s", f.lastSQL)
	}
}

func TestHermesPGFingerprintParsesCountAndNewest(t *testing.T) {
	f := &fakePG{fingerprint: "42|1785000000"}
	defer SetHermesPGRunner(f.run)()

	rows, newest, err := HermesPGFingerprint("postgres://x")
	if err != nil {
		t.Fatal(err)
	}
	if rows != 42 {
		t.Errorf("rows = %d", rows)
	}
	if newest != 1785000000*1_000_000_000 {
		t.Errorf("newest nano = %d", newest)
	}
}

// deja reads Hermes' epoch-seconds timestamp. A store whose column is some
// other type (a timestamptz text like `2026-07-21 10:00:00+00`) must fail
// loudly: indexing with a zero watermark would silently miss every later row.
func TestHermesPGFingerprintRejectsAnUnreadableTimestamp(t *testing.T) {
	f := &fakePG{fingerprint: "7|2026-07-21 10:00:00+00"}
	defer SetHermesPGRunner(f.run)()

	if _, _, err := HermesPGFingerprint("postgres://x"); err == nil {
		t.Fatal("an unreadable max(timestamp) was accepted")
	} else if !strings.Contains(err.Error(), "epoch seconds") {
		t.Errorf("error does not explain the format: %v", err)
	}
}

// A fractional epoch second is the shape Hermes actually writes, and it must
// parse.
func TestHermesPGFingerprintReadsFractionalEpoch(t *testing.T) {
	f := &fakePG{fingerprint: "3|1785000000.5"}
	defer SetHermesPGRunner(f.run)()

	rows, newest, err := HermesPGFingerprint("postgres://x")
	if err != nil {
		t.Fatal(err)
	}
	if rows != 3 || newest <= 0 {
		t.Errorf("rows=%d newest=%d", rows, newest)
	}
}

// The DSN can carry a password; the token that stands in for the store must not
// leak it, and it must be stable so the index recognises the same store twice.
func TestHermesPGStorePathHidesTheDSN(t *testing.T) {
	dsn := "postgres://user:s3cret@host:55432/hermes" //nolint:gosec // a made-up DSN the test redacts
	tok := HermesPGStorePath(dsn)
	if strings.Contains(tok, "s3cret") || strings.Contains(tok, "host") {
		t.Fatalf("token leaked the DSN: %q", tok)
	}
	if tok != HermesPGStorePath(dsn) {
		t.Fatal("token is not stable")
	}
	if !IsHermesPGStore(tok) {
		t.Fatalf("%q not recognised as a pg store", tok)
	}
}

// The Postgres store has the same sessions table, and a session read from it
// carries who started it as one read from SQLite does (#4948).
func TestParseHermesPGReadsSessionKinds(t *testing.T) {
	f := &fakePG{
		rows: `[
		{"session_id":"s-cli","role":"user","content":"plan the migration","timestamp":1785000000},
		{"session_id":"s-sub","role":"user","content":"read the schema","timestamp":1785000010},
		{"session_id":"s-cron","role":"user","content":"nightly digest","timestamp":1785000020}
	]`,
		columns:  `["id","source","parent_session_id","cwd","model"]`,
		sessions: `[{"id":"s-cli","source":"cli","parent":null,"cwd":"/w/app"},{"id":"s-sub","source":"subagent","parent":"s-cli","cwd":"/w/app"},{"id":"s-cron","source":"cron","parent":null,"cwd":null}]`,
	}
	defer SetHermesPGRunner(f.run)()
	ss, err := ParseHermesPG("postgres://deja@192.0.2.1/hermes", 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range ss {
		got[s.ID] = s.Kind + "/" + s.Parent + "/" + s.Project
	}
	want := map[string]string{"s-cli": "//w/app", "s-sub": "subagent/s-cli/w/app", "s-cron": "cron//hermes"}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s = %q, want %q", id, got[id], w)
		}
	}
}

// A Hermes session is about the directory it was recorded in — the store's
// sessions.cwd — not the profile that ran it. Stamped with the profile, every
// session fell into one project and the prompt hook in the real directory
// never served it (#3257). The name follows the other parsers: the last two
// segments of a path that is not on this machine.
func TestHermesProjectComesFromTheStoresCwd(t *testing.T) {
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
		CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT NOT NULL, started_at REAL NOT NULL, cwd TEXT, title TEXT);
		INSERT INTO sessions (id,source,started_at,cwd) VALUES
		 ('routed','cli',1785000000.0,'/home/qa/work/widgetd'),
		 ('nowhere','telegram',1785000000.0,NULL),
		 ('blank','cli',1785000000.0,'');
		INSERT INTO messages (session_id,role,content,timestamp) VALUES
		 ('routed','user','why does the import drop the second profile',1785000010.0),
		 ('nowhere','user','what is the weather in the mountains',1785000020.0),
		 ('blank','user','remind me of the plan',1785000030.0),
		 ('orphan','user','a session the sessions table never recorded',1785000040.0);`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v\n%s", err, out)
	}
	ss, err := ParseHermesDB(db)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := map[string]string{}
	for _, s := range ss {
		got[s.ID] = s.Project
	}
	want := map[string]string{"routed": "work/widgetd", "nowhere": "writer", "blank": "writer", "orphan": "writer"}
	for id, p := range want {
		if got[id] != p {
			t.Errorf("session %s project = %q, want %q", id, got[id], p)
		}
	}
}

// The watermark is the newest message deja has, and the filter has to hand back
// everything after it — including the messages written in the same instant.
//
// The column is REAL seconds, and the filter compared against whole seconds
// with a strict `>`. On a store that writes fractional stamps that over-reads
// harmlessly; on one that writes whole seconds, every other message of the
// watermark's own second is skipped, permanently. Measured before the fix:
// whole seconds handed back 0 of 2, fractional handed back 2 of 2 (#2075).
func TestHermesHandsBackTheWatermarksOwnSecond(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	const at = int64(1767268800)
	for _, tc := range []struct{ name, a, b string }{
		{"whole seconds", "1767268800", "1767268800"},
		{"fractional", "1767268800.2", "1767268800.7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "state.db")
			stmts := `create table messages (id integer primary key, session_id text, role text, content text, timestamp real);` +
				`insert into messages values (1,'s1','user','first turn',` + tc.a + `);` +
				`insert into messages values (2,'s1','user','second turn',` + tc.b + `);`
			if out, err := exec.CommandContext(t.Context(), "sqlite3", db, stmts).CombinedOutput(); err != nil {
				t.Fatalf("seed: %v %s", err, out)
			}
			ss, err := ParseHermesDBSince(db, time.Unix(at, 0))
			if err != nil {
				t.Fatal(err)
			}
			var text []string
			for _, s := range ss {
				for _, m := range s.Messages {
					text = append(text, m.Text)
				}
			}
			joined := strings.Join(text, " | ")
			if !strings.Contains(joined, "second turn") {
				t.Errorf("a message stamped in the watermark's own second was skipped: %q", joined)
			}
		})
	}
}

// The sessions table as Hermes 0.17 writes it for delegate_task: the child's
// row has no cwd (only a cli row records one), its source is the parent's
// when the parent ran under --source, the TUI or a gateway, and
// model_config._delegate_from names the parent even after a delete clears
// parent_session_id (tools/delegate_tool.py).
func TestHermesSubagentHangsUnderItsParentAndFilesItsWritesThere(t *testing.T) {
	db := writeHermesDB(t, filepath.Join(t.TempDir(), "p"), `
		CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT, parent_session_id TEXT, cwd TEXT, model_config TEXT);
		INSERT INTO sessions VALUES
		 ('20261018_084704_a4b1b7','cli',NULL,'/w/app','{"max_iterations":90}'),
		 ('20261018_084719_441ac7','subagent','20261018_084704_a4b1b7',NULL,'{"_delegate_from":"20261018_084704_a4b1b7"}'),
		 ('20261018_084938_89405b','tool',NULL,NULL,'{"max_iterations":90}'),
		 ('20261018_084941_17754d','tool','20261018_084938_89405b',NULL,'{"_delegate_from":"20261018_084938_89405b"}'),
		 ('20261018_085000_cccccc','subagent',NULL,NULL,'{"_delegate_from":"20261018_084704_a4b1b7"}'),
		 ('20261018_085100_dddddd','cli','20261018_084704_a4b1b7','/w/app','{"max_iterations":90}');
		INSERT INTO messages (session_id,role,content,tool_call_id,tool_calls,tool_name,timestamp) VALUES
		 ('20261018_084704_a4b1b7','user','write the parent file',NULL,NULL,NULL,1792302400.0),
		 ('20261018_084704_a4b1b7','assistant',NULL,NULL,'[{"id":"c1","type":"function","function":{"name":"write_file","arguments":"{\"path\": \"parent.txt\", \"content\": \"PARENT_LINE written by the parent session\\n\"}"}}]',NULL,1792302401.0),
		 ('20261018_084719_441ac7','user','write the child file',NULL,NULL,NULL,1792302410.0),
		 ('20261018_084719_441ac7','assistant',NULL,NULL,'[{"id":"c2","type":"function","function":{"name":"write_file","arguments":"{\"path\": \"child.txt\", \"content\": \"CHILD_LINE written by the delegated subagent\\n\"}"}}]',NULL,1792302411.0),
		 ('20261018_084719_441ac7','assistant',NULL,NULL,'[{"id":"c3","type":"function","function":{"name":"patch","arguments":"{\"mode\": \"patch\", \"patch\": \"*** Begin Patch\\n*** Update File: sub/a.py\\n@@\\n-old_value = compute_the_old_way()\\n+new_value = compute_the_new_way()\\n*** End Patch\"}"}}]',NULL,1792302412.0),
		 ('20261018_084719_441ac7','assistant',NULL,NULL,'[{"id":"c4","type":"function","function":{"name":"write_file","arguments":"{\"path\": \"~/notes/todo.md\", \"content\": \"a line in the home directory, left as written\\n\"}"}},{"id":"c5","type":"function","function":{"name":"write_file","arguments":"{\"path\": \"/elsewhere/abs.txt\", \"content\": \"an absolute path stays exactly as it was\\n\"}"}}]',NULL,1792302413.0),
		 ('20261018_084938_89405b','user','delegate from a tool session',NULL,NULL,NULL,1792302420.0),
		 ('20261018_084941_17754d','user','the tool session child',NULL,NULL,NULL,1792302421.0),
		 ('20261018_084941_17754d','assistant',NULL,NULL,'[{"id":"c6","type":"function","function":{"name":"write_file","arguments":"{\"path\": \"tool-child.txt\", \"content\": \"a session with no cwd keeps the path it wrote\\n\"}"}}]',NULL,1792302422.0),
		 ('20261018_085000_cccccc','user','a child whose parent row was deleted',NULL,NULL,NULL,1792302430.0),
		 ('20261018_085100_dddddd','user','summary of the earlier turns',NULL,NULL,NULL,1792302440.0);`)
	ss, err := ParseHermesDB(db)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	heads := map[string][]string{}
	for _, s := range ss {
		got[s.ID] = s.Kind + "/" + s.Parent + "/" + s.Project
		for _, m := range s.Messages {
			if m.Role == RoleWrote || m.Role == RoleEdit || m.Role == RoleFiles {
				head, _, _ := strings.Cut(m.Text, "\n")
				if m.Role == RoleFiles {
					head = m.Text
				}
				heads[s.ID] = append(heads[s.ID], m.Role+" "+head)
			}
		}
	}
	want := map[string]string{
		"20261018_084704_a4b1b7": "//w/app",
		"20261018_084719_441ac7": "subagent/20261018_084704_a4b1b7/w/app",
		"20261018_084938_89405b": "//p",
		"20261018_084941_17754d": "subagent/20261018_084938_89405b/p",
		"20261018_085000_cccccc": "subagent/20261018_084704_a4b1b7/w/app",
		// A compression's continuation names its parent without the marker.
		"20261018_085100_dddddd": "//w/app",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s = %q, want %q", id, got[id], w)
		}
	}
	wantHeads := map[string]string{
		"20261018_084704_a4b1b7": "files /w/app/parent.txt|wrote /w/app/parent.txt",
		"20261018_084719_441ac7": "files /w/app/child.txt|wrote /w/app/child.txt|" +
			"files /w/app/sub/a.py|wrote /w/app/sub/a.py|edit /w/app/sub/a.py|" +
			"files ~/notes/todo.md|wrote ~/notes/todo.md|" +
			"files /elsewhere/abs.txt|wrote /elsewhere/abs.txt",
		"20261018_084941_17754d": "files tool-child.txt|wrote tool-child.txt",
	}
	for id, w := range wantHeads {
		if g := strings.Join(heads[id], "|"); g != w {
			t.Errorf("%s records:\n got %s\nwant %s", id, g, w)
		}
	}
}

func writeHermesDB(t *testing.T, dir string, rows string) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is not installed")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "state.db")
	schema := `CREATE TABLE messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		role TEXT NOT NULL,
		content TEXT,
		tool_call_id TEXT,
		tool_calls TEXT,
		tool_name TEXT,
		timestamp REAL NOT NULL,
		token_count INTEGER,
		finish_reason TEXT);` + rows
	cmd := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd.Stdin = strings.NewReader(schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v\n%s", err, out)
	}
	return db
}

func TestParseHermesDB(t *testing.T) {
	root := t.TempDir()
	db := writeHermesDB(t, filepath.Join(root, "architect"), `
		INSERT INTO messages (session_id,role,content,timestamp) VALUES
		 ('sess-1','user','fix the makeslice panic',1785000000.5),
		 ('sess-1','assistant','bound the length against the file',1785000060.0),
		 ('sess-1','tool',NULL,1785000061.0),
		 ('sess-2','user','second session',1785100000.0);`)
	ss, err := ParseHermesDB(db)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(ss) != 2 {
		t.Fatalf("sessions = %d, want 2: %+v", len(ss), ss)
	}
	first := ss[0]
	if first.Harness != "hermes" || first.ID != "sess-1" {
		t.Fatalf("session identity = %q/%q", first.Harness, first.ID)
	}
	// Tool rows carry no prose and must not become messages.
	if len(first.Messages) != 2 {
		t.Fatalf("messages = %d, want 2: %+v", len(first.Messages), first.Messages)
	}
	if first.Messages[0].Role != "user" || first.Messages[0].Text != "fix the makeslice panic" {
		t.Fatalf("first message = %+v", first.Messages[0])
	}
	// timestamp is REAL seconds; a session with a zero time sorts as ancient
	// and never surfaces in recall.
	if first.Messages[0].Time.IsZero() || first.Updated.IsZero() {
		t.Fatalf("timestamps not parsed: %+v", first)
	}
	if got := first.Messages[0].Time.UTC().Year(); got != 2026 {
		t.Fatalf("timestamp year = %d, want 2026", got)
	}
	// The profile directory is the only grouping Hermes has.
	if first.Project != "architect" {
		t.Fatalf("project = %q, want the profile name", first.Project)
	}
	// The title is the index's to derive (#3251).
	if first.Title != "" {
		t.Fatalf("title = %q, want none", first.Title)
	}
}

func TestHermesDBsFindsEveryProfile(t *testing.T) {
	root := t.TempDir()
	writeHermesDB(t, filepath.Join(root, "architect"), `
		INSERT INTO messages (session_id,role,content,timestamp) VALUES ('a','user','x',1785000000);`)
	writeHermesDB(t, filepath.Join(root, "researcher"), `
		INSERT INTO messages (session_id,role,content,timestamp) VALUES ('b','user','y',1785000000);`)
	// An empty store must not be listed: statting it every build is waste.
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "empty", "state.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// Without a fake home this reaches the developer's own ~/.hermes.
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("DEJA_HERMES_HOME", filepath.Join(root, "empty-home"))
	t.Setenv("DEJA_HERMES_PROFILES_ROOT", root)
	t.Setenv("DEJA_HERMES_DB", "")
	if got := HermesDBs(); len(got) != 2 {
		t.Fatalf("stores = %v, want the two non-empty profiles", got)
	}
	if got := LoadHermes(); len(got) != 2 {
		t.Fatalf("sessions = %d, want one per profile", len(got))
	}
}

func TestParseHermesDBSinceReadsOnlyNewRows(t *testing.T) {
	root := t.TempDir()
	db := writeHermesDB(t, filepath.Join(root, "architect"), `
		INSERT INTO messages (session_id,role,content,timestamp) VALUES
		 ('old','user','ancient',1700000000),
		 ('new','user','recent',1785000000);`)
	ss, err := ParseHermesDBSince(db, time.Unix(1780000000, 0))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(ss) != 1 || ss[0].ID != "new" {
		t.Fatalf("sessions = %+v, want only the row past the watermark", ss)
	}
}

func TestParseHermesDBHandlesMissingAndEmpty(t *testing.T) {
	if ss, err := ParseHermesDB(filepath.Join(t.TempDir(), "nope.db")); err != nil || ss != nil {
		t.Fatalf("missing store = %v, %v", ss, err)
	}
	empty := filepath.Join(t.TempDir(), "empty.db")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if ss, err := ParseHermesDB(empty); err != nil || ss != nil {
		t.Fatalf("empty store = %v, %v", ss, err)
	}
}

// Hermes 0.17 keeps one store at ~/.hermes/state.db; older builds shard it per
// profile. Reading only the profile layout meant a current install indexed
// nothing at all, silently.
func TestHermesFindsTheRootStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_HERMES_HOME", "")
	t.Setenv("HERMES_HOME", "")
	t.Setenv("DEJA_HERMES_DB", "")
	t.Setenv("DEJA_HERMES_PROFILES_ROOT", "")
	root := filepath.Join(home, ".hermes")
	writeHermesDB(t, root, `
		INSERT INTO messages (session_id,role,content,timestamp) VALUES ('flat','user','root store',1785000000);`)
	writeHermesDB(t, filepath.Join(root, "profiles", "architect"), `
		INSERT INTO messages (session_id,role,content,timestamp) VALUES ('per-profile','user','profile store',1785000000);`)
	got := HermesDBs()
	if len(got) != 2 {
		t.Fatalf("stores = %v, want both layouts", got)
	}
	ss := LoadHermes()
	if len(ss) != 2 {
		t.Fatalf("sessions = %d, want one from each store", len(ss))
	}
	if newest, err := ParseHermesNewest(got[0]); err != nil || len(newest) != 1 {
		t.Fatalf("ParseHermesNewest = %v, %#v", err, newest)
	}
	if HermesStoreLacks(got[0], "flat") || !HermesStoreLacks(got[0], "missing") {
		t.Fatalf("HermesStoreLacks unexpected result")
	}
	// The root store has no profile directory to be named after.
	names := make([]string, 0, len(ss))
	for _, s := range ss {
		names = append(names, s.Project)
	}
	if !contains(names, "hermes") || !contains(names, "architect") {
		t.Fatalf("projects = %v", names)
	}
	for _, harness := range Registry() {
		if harness.Name == "hermes" {
			match := harness.Kinds[0].Match
			if !match(got[0]) || !match(got[1]) || match(filepath.Join(root, "state.db.backup")) {
				t.Fatalf("registry did not recognize both Hermes store layouts: %v", got)
			}
			return
		}
	}
	t.Fatal("Hermes missing from source registry")
}

func contains(ss []string, want string) bool {
	return slices.Contains(ss, want)
}

// A session Hermes opens with its own greeting was titled by that greeting
// (#3241); titling in the parser then skipped the index's rule for a person's
// greeting (#3251). The parser leaves the title empty, and the index derives it
// the way it does for every other harness.
func TestHermesLeavesTheTitleToTheIndex(t *testing.T) {
	root := t.TempDir()
	db := writeHermesDB(t, filepath.Join(root, "writer"), `
		INSERT INTO messages (session_id,role,content,timestamp) VALUES
		 ('greet','assistant','hello, what shall we do about the ptarmigan cache',1785000000.0),
		 ('greet','user','why does the ptarmigan cache miss on restart',1785000010.0),
		 ('bot','assistant','nobody typed here',1785100000.0);`)
	ss, err := ParseHermesDB(db)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, s := range ss {
		if s.Title != "" {
			t.Errorf("session %s titled %q in the parser", s.ID, s.Title)
		}
	}
}

// The rows Hermes 0.17 wrote for a session that ran terminal, write_file and
// patch: the calls ride on assistant rows with no content, the results on
// `tool` rows. None of it reached the index (#4242).
func TestHermesToolCallsBecomeWorkRecords(t *testing.T) {
	db := writeHermesDB(t, filepath.Join(t.TempDir(), "p"), `
		INSERT INTO messages (session_id,role,content,tool_call_id,tool_calls,tool_name,timestamp) VALUES
		 ('s','user','fix the retry loop',NULL,NULL,NULL,1785000000.0),
		 ('s','assistant','',NULL,'[{"id":"c1","type":"function","function":{"name":"terminal","arguments":"{\"command\": \"go test ./...\"}"}}]',NULL,1785000001.0),
		 ('s','tool','{"output": "notes.txt", "exit_code": 0, "error": null}','c1',NULL,'terminal',1785000002.0),
		 ('s','assistant',NULL,NULL,'[{"id":"c2","type":"function","function":{"name":"write_file","arguments":"{\"path\": \"/tmp/proj/retry.py\", \"content\": \"def retry_with_backoff(attempts):\\n    raise NotImplementedError\\n\"}"}}]',NULL,1785000003.0),
		 ('s','tool','{"bytes_written": 26}','c2',NULL,'write_file',1785000004.0),
		 ('s','assistant','',NULL,'[{"id":"c3","type":"function","function":{"name":"patch","arguments":"{\"mode\": \"replace\", \"path\": \"/tmp/proj/retry.py\", \"old_string\": \"raise NotImplementedError\", \"new_string\": \"return attempts * backoff_seconds\"}"}}]',NULL,1785000005.0),
		 ('s','assistant','',NULL,'[{"id":"c4","type":"function","function":{"name":"terminal","arguments":"{\"command\": \"make build\"}"}}]',NULL,1785000006.0),
		 ('s','tool','{"output": "SyntaxError: bad", "exit_code": 1, "error": null}','c4',NULL,'terminal',1785000007.0),
		 ('s','assistant','',NULL,'[{"id":"c5","type":"function","function":{"name":"patch","arguments":"{\"mode\": \"patch\", \"patch\": \"*** Begin Patch\\n*** Update File: /tmp/proj/a.py\\n@@\\n-old_value = compute_the_old_way()\\n+new_value = compute_the_new_way()\\n*** End Patch\"}"}}]',NULL,1785000008.0),
		 ('s','assistant','Done.',NULL,NULL,NULL,1785000009.0);`)
	ss, err := ParseHermesDB(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	by := map[string][]string{}
	for _, m := range ss[0].Messages {
		by[m.Role] = append(by[m.Role], m.Text)
	}
	want := map[string][]string{
		RoleCommand:    {"$ go test ./...  → exit 0", "$ make build  → exit 1"},
		RoleFiles:      {"/tmp/proj/retry.py", "/tmp/proj/retry.py", "/tmp/proj/a.py"},
		RoleEdit:       {"/tmp/proj/retry.py\nraise NotImplementedError", "/tmp/proj/a.py\nold_value = compute_the_old_way()"},
		RoleToolOutput: {"notes.txt", "SyntaxError: bad"},
	}
	for role, w := range want {
		if got := strings.Join(by[role], " | "); got != strings.Join(w, " | ") {
			t.Errorf("%s = %q, want %q", role, by[role], w)
		}
	}
	// write_file's content, patch's new_string, and the patch's added line.
	if len(by[RoleWrote]) != 3 {
		t.Errorf("wrote records = %d, want 3: %q", len(by[RoleWrote]), by[RoleWrote])
	}
	if got := strings.Join(by["assistant"], "|"); got != "Done." {
		t.Errorf("assistant prose = %q, want the one real line", got)
	}
}

// hermes017Schema is the messages table as Hermes 0.17 creates it
// (hermes_state.py), with the active/compacted pair rewind and compaction set.
const hermes017Schema = `CREATE TABLE messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL,
	role TEXT NOT NULL,
	content TEXT,
	tool_call_id TEXT,
	tool_calls TEXT,
	tool_name TEXT,
	timestamp REAL NOT NULL,
	active INTEGER NOT NULL DEFAULT 1,
	compacted INTEGER NOT NULL DEFAULT 0);`

func writeHermesStore(t *testing.T, schema, rows string) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is not installed")
	}
	dir := filepath.Join(t.TempDir(), "p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "state.db")
	cmd := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd.Stdin = strings.NewReader(schema + rows)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v\n%s", err, out)
	}
	return db
}

func hermesRoles(t *testing.T, db string) map[string][]string {
	t.Helper()
	ss, err := ParseHermesDB(db)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	by := map[string][]string{}
	for _, s := range ss {
		for _, m := range s.Messages {
			by[m.Role] = append(by[m.Role], m.Text)
		}
	}
	return by
}

// exit_code -1 is what terminal returns for a command it never ran — denied,
// blocked, waiting on approval, invalid, failed to start
// (tools/terminal_tool.py). Recorded as a command, it read as one that ran.
func TestHermesCommandThatNeverRanIsNoCommand(t *testing.T) {
	db := writeHermesStore(t, hermes017Schema, `
		INSERT INTO messages (session_id,role,content,tool_call_id,tool_calls,tool_name,timestamp) VALUES
		 ('s','user','clean the build dir',NULL,NULL,NULL,1785000000.0),
		 ('s','assistant','',NULL,'[{"id":"c1","type":"function","function":{"name":"terminal","arguments":"{\"command\": \"rm -rf build && make build\"}"}}]',NULL,1785000001.0),
		 ('s','tool','{"output": "", "exit_code": -1, "error": "Command denied: recursive delete.", "status": "blocked"}','c1',NULL,'terminal',1785000002.0),
		 ('s','assistant','',NULL,'[{"id":"c2","type":"function","function":{"name":"terminal","arguments":"{\"command\": \"make build\"}"}}]',NULL,1785000003.0),
		 ('s','tool','{"output": "ok", "exit_code": 0, "error": null}','c2',NULL,'terminal',1785000004.0);`)
	by := hermesRoles(t, db)
	if got := strings.Join(by[RoleCommand], "|"); got != "$ make build  → exit 0" {
		t.Errorf("commands = %q, want only the one that ran", by[RoleCommand])
	}
	if !strings.Contains(strings.Join(by[RoleToolOutput], "|"), "Command denied") {
		t.Errorf("tool output lost why it never ran: %q", by[RoleToolOutput])
	}
}

// Multimodal content is stored as "\x00json:" + the parts (hermes_state.py
// _encode_content). The text parts are the message; the image is base64.
func TestHermesMultimodalKeepsTextParts(t *testing.T) {
	parts := `[{"type": "text", "text": "why is this chart flat"}, {"type": "image_url", "image_url": {"url": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB"}}]`
	db := writeHermesStore(t, hermes017Schema, `
		INSERT INTO messages (session_id,role,content,tool_call_id,tool_calls,tool_name,timestamp) VALUES
		 ('s','user',char(0)||'json:'||'`+parts+`',NULL,NULL,NULL,1785000000.0),
		 ('s','tool',char(0)||'json:'||'`+strings.ReplaceAll(parts, "why is this chart flat", "screenshot of the dashboard")+`','c1',NULL,'computer_use',1785000001.0);`)
	by := hermesRoles(t, db)
	if got := strings.Join(by["user"], "|"); got != "why is this chart flat" {
		t.Errorf("user = %q", got)
	}
	if got := strings.Join(by[RoleToolOutput], "|"); got != "screenshot of the dashboard" {
		t.Errorf("tool output = %q", got)
	}
}

// A result is JSON. What it says lives in output, content, diff or error;
// the rest is bookkeeping, and keys starting with _ are hints to the model.
// Compressor stubs stand in for output that was cleared and say nothing.
func TestHermesToolResultIsItsText(t *testing.T) {
	db := writeHermesStore(t, hermes017Schema, `
		INSERT INTO messages (session_id,role,content,tool_call_id,tool_calls,tool_name,timestamp) VALUES
		 ('s','user','read the config',NULL,NULL,NULL,1785000000.0),
		 ('s','tool','{"content": "1|retries: 3\n2|Always retry on 503", "total_lines": 2, "_hint": "use offset to read more"}','c1',NULL,'read_file',1785000001.0),
		 ('s','tool','{"success": true, "diff": "-retries: 3\n+retries: 5", "files_modified": ["/x"]}','c2',NULL,'patch',1785000002.0),
		 ('s','tool','{"bytes_written": 50, "dirs_created": true}','c3',NULL,'write_file',1785000003.0),
		 ('s','tool','{"success": false, "error": "file not found: /nope"}','c4',NULL,'read_file',1785000004.0),
		 ('s','tool','[Old tool output cleared to save context space]','c5',NULL,'terminal',1785000005.0),
		 ('s','tool','[terminal] ran `+"`make`"+` -> exit 0, 40 lines output','c6',NULL,'terminal',1785000006.0),
		 ('s','tool','[Duplicate tool output — same content as a more recent call]','c7',NULL,'read_file',1785000007.0);`)
	by := hermesRoles(t, db)
	want := []string{"1|retries: 3\n2|Always retry on 503", "-retries: 3\n+retries: 5", "file not found: /nope"}
	if got := by[RoleToolOutput]; strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("tool output = %q, want %q", got, want)
	}
}

// Compaction archives the live rows (active=0, compacted=1) and writes the
// summary and the kept tail again as new active rows; rewind takes turns back (active=0,
// compacted=0). The first must not count a call twice, the second must not
// count at all (hermes_state.py archive_and_compact, rewind_to_message).
func TestHermesCompactionAndRewind(t *testing.T) {
	call := func(id, path string) string {
		return `'[{"id":"` + id + `","type":"function","function":{"name":"patch","arguments":"{\"mode\": \"replace\", \"path\": \"` + path + `\", \"old_string\": \"retries = compute_old_retry_budget()\", \"new_string\": \"retries = compute_new_retry_budget()\"}"}}]'`
	}
	db := writeHermesStore(t, hermes017Schema, `
		INSERT INTO messages (session_id,role,content,tool_call_id,tool_calls,tool_name,timestamp,active,compacted) VALUES
		 ('s','user','fix the retry budget',NULL,NULL,NULL,1785000000.0,0,1),
		 ('s','assistant','',NULL,`+call("c1", "/w/a.py")+`,NULL,1785000001.0,0,1),
		 ('s','tool','{"success": true, "diff": "-old\n+new"}','c1',NULL,'patch',1785000002.0,0,1),
		 ('s','user','[CONTEXT COMPACTION — REFERENCE ONLY] Earlier turns were compacted into the summary below.',NULL,NULL,NULL,1785000010.0,1,0),
		 ('s','assistant','',NULL,`+call("c1", "/w/a.py")+`,NULL,1785000010.0,1,0),
		 ('s','tool','[Old tool output cleared to save context space]','c1',NULL,'patch',1785000010.0,1,0),
		 ('s','user','try b.py instead',NULL,NULL,NULL,1785000020.0,0,0),
		 ('s','assistant','',NULL,`+call("c9", "/w/b.py")+`,NULL,1785000021.0,0,0);`)
	by := hermesRoles(t, db)
	if got := by[RoleEdit]; len(got) != 1 || !strings.HasPrefix(got[0], "/w/a.py\n") {
		t.Errorf("edits = %q, want a.py once and no rewound b.py", got)
	}
	if got := strings.Join(by[RoleFiles], "|"); got != "/w/a.py" {
		t.Errorf("files = %q", got)
	}
	if got := strings.Join(by["user"], "|"); got != "fix the retry budget" {
		t.Errorf("user = %q, want the rewound turn left out", got)
	}
	if got := strings.Join(by[RoleToolOutput], "|"); got != "-old\n+new" {
		t.Errorf("tool output = %q", got)
	}
}

// V4A as Hermes' own parser takes it (tools/patch_parser.py): spacing after
// *** is optional, and Move File names two paths.
func TestHermesPatchHeadersAsHermesReadsThem(t *testing.T) {
	patch := `*** Begin Patch\\n***Update File: /w/a.py\\n@@\\n-old_value = compute_the_old_way()\\n+new_value = compute_the_new_way()\\n*** Move File: /w/b.py -> /w/c.py\\n*** End Patch`
	db := writeHermesStore(t, hermes017Schema, `
		INSERT INTO messages (session_id,role,content,tool_call_id,tool_calls,tool_name,timestamp) VALUES
		 ('s','user','move it',NULL,NULL,NULL,1785000000.0),
		 ('s','assistant','',NULL,'[{"id":"c1","type":"function","function":{"name":"patch","arguments":"{\"mode\": \"patch\", \"patch\": \"`+patch+`\"}"}}]',NULL,1785000001.0);`)
	by := hermesRoles(t, db)
	if got := strings.Join(by[RoleFiles], "|"); got != "/w/a.py\n/w/b.py\n/w/c.py" {
		t.Errorf("files = %q", got)
	}
	if got := strings.Join(by[RoleEdit], "|"); got != "/w/a.py\nold_value = compute_the_old_way()" {
		t.Errorf("edits = %q", got)
	}
	if len(by[RoleWrote]) != 1 {
		t.Errorf("wrote = %q", by[RoleWrote])
	}
}

// tool_calls is json.dumps of whatever the caller passed, and a single call
// can arrive as a dict rather than a list (hermes_state.py append_message).
func TestHermesToolCallsAsOneObject(t *testing.T) {
	db := writeHermesStore(t, hermes017Schema, `
		INSERT INTO messages (session_id,role,content,tool_call_id,tool_calls,tool_name,timestamp) VALUES
		 ('s','user','run the tests',NULL,NULL,NULL,1785000000.0),
		 ('s','assistant','',NULL,'{"id":"c1","type":"function","function":{"name":"terminal","arguments":"{\"command\": \"go test ./...\"}"}}',NULL,1785000001.0);`)
	by := hermesRoles(t, db)
	if got := strings.Join(by[RoleCommand], "|"); got != "$ go test ./..." {
		t.Errorf("commands = %q", got)
	}
}

// A store from before the tool columns still gives its prose: naming a
// missing column would fail the whole query.
func TestHermesStoreWithoutToolColumns(t *testing.T) {
	db := writeHermesStore(t, `CREATE TABLE messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL,
		role TEXT NOT NULL, content TEXT, timestamp REAL NOT NULL);`, `
		INSERT INTO messages (session_id,role,content,timestamp) VALUES
		 ('s','user','an old question',1785000000.0),
		 ('s','assistant','an old answer',1785000001.0);`)
	by := hermesRoles(t, db)
	if got := strings.Join(by["user"], "|") + "/" + strings.Join(by["assistant"], "|"); got != "an old question/an old answer" {
		t.Errorf("prose = %q", got)
	}
}

// Hermes fills a missing call id with sha256(name:args:index)
// (agent/chat_completion_helpers.py), so the same command run twice carries
// the same id. Only a call compaction wrote again is a repeat.
func TestHermesRepeatedCallIDKeepsBothRuns(t *testing.T) {
	call := `'[{"id":"call_3f1a2b4c5d6e","type":"function","function":{"name":"terminal","arguments":"{\"command\": \"go test ./...\"}"}}]'`
	db := writeHermesStore(t, hermes017Schema, `
		INSERT INTO messages (session_id,role,content,tool_call_id,tool_calls,tool_name,timestamp) VALUES
		 ('s','user','make the tests pass',NULL,NULL,NULL,1785000000.0),
		 ('s','assistant','',NULL,`+call+`,NULL,1785000001.0),
		 ('s','tool','{"output": "FAIL pool_test.go:12", "exit_code": 1}','call_3f1a2b4c5d6e',NULL,'terminal',1785000002.0),
		 ('s','assistant','',NULL,`+call+`,NULL,1785000010.0),
		 ('s','tool','{"output": "ok  example/pool 0.2s", "exit_code": 0}','call_3f1a2b4c5d6e',NULL,'terminal',1785000011.0);`)
	by := hermesRoles(t, db)
	if got := strings.Join(by[RoleCommand], "|"); got != "$ go test ./...  → exit 1|$ go test ./...  → exit 0" {
		t.Errorf("commands = %q", by[RoleCommand])
	}
	if got := strings.Join(by[RoleToolOutput], "|"); got != "FAIL pool_test.go:12|ok  example/pool 0.2s" {
		t.Errorf("tool output = %q", by[RoleToolOutput])
	}
}

// Results that hold their text somewhere other than the known keys keep it:
// search_files in files mode, web_search, web_extract, delegate_task, and an
// output that is not a string.
func TestHermesStructuredResultsKeepTheirText(t *testing.T) {
	db := writeHermesStore(t, hermes017Schema, `
		INSERT INTO messages (session_id,role,content,tool_call_id,tool_calls,tool_name,timestamp) VALUES
		 ('s','user','where is the retry budget',NULL,NULL,NULL,1785000000.0),
		 ('s','tool','{"total_count": 1, "files": ["/w/retry_budget.go"]}','c1',NULL,'search_files',1785000001.0),
		 ('s','tool','{"total_count": 1, "matches": [{"path": "/w/a.go", "line": 3, "content": "computeRetryBudget()"}]}','c2',NULL,'search_files',1785000002.0),
		 ('s','tool','{"success": true, "data": {"web": [{"title": "Retry budgets in gRPC", "url": "https://example.com"}]}}','c3',NULL,'web_search',1785000003.0),
		 ('s','tool','{"results": [{"url": "https://example.com", "content": "extracted page body"}]}','c4',NULL,'web_extract',1785000004.0),
		 ('s','tool','{"results": [{"task_index": 0, "status": "completed", "summary": "the leak is in pool.go"}]}','c5',NULL,'delegate_task',1785000005.0),
		 ('s','tool','{"output": 42}','c6',NULL,'execute_code',1785000006.0),
		 ('s','tool','{"output": ["line one", "line two"], "exit_code": 0}','c7',NULL,'terminal',1785000007.0),
		 ('s','tool','{"bytes_written": 26, "_warning": "file was overwritten"}','c8',NULL,'write_file',1785000008.0);`)
	got := strings.Join(hermesRoles(t, db)[RoleToolOutput], "|")
	for _, want := range []string{"/w/retry_budget.go", "computeRetryBudget()", "Retry budgets in gRPC",
		"extracted page body", "the leak is in pool.go", "42", "line one\nline two"} {
		if !strings.Contains(got, want) {
			t.Errorf("tool output %q lost %q", got, want)
		}
	}
	if strings.Contains(got, "overwritten") {
		t.Errorf("tool output %q kept a hint meant for the model", got)
	}
}

// -1 is a command that never ran; any other negative code is a process killed
// by a signal after it started (tools/environments/base.py), which ran.
func TestHermesKilledCommandStaysACommand(t *testing.T) {
	db := writeHermesStore(t, hermes017Schema, `
		INSERT INTO messages (session_id,role,content,tool_call_id,tool_calls,tool_name,timestamp) VALUES
		 ('s','user','build it',NULL,NULL,NULL,1785000000.0),
		 ('s','assistant','',NULL,'[{"id":"c1","type":"function","function":{"name":"terminal","arguments":"{\"command\": \"make all\"}"}}]',NULL,1785000001.0),
		 ('s','tool','{"output": "cc1: out of memory", "exit_code": -9}','c1',NULL,'terminal',1785000002.0);`)
	if got := strings.Join(hermesRoles(t, db)[RoleCommand], "|"); got != "$ make all" {
		t.Errorf("commands = %q, want the killed run kept", got)
	}
}

// After Move File and Delete File, Hermes' parser has no current file and
// ignores lines until the next header (tools/patch_parser.py).
func TestHermesPatchLinesAfterMoveBelongToNoFile(t *testing.T) {
	patch := `***Begin Patch\\n*** Move File: /w/b.py -> /w/c.py\\n-ghost_line_hermes_ignores = 1\\n*** Delete File: /w/d.py\\n-another_ignored_line = 2\\n*** Update File: /w/a.py\\n@@\\n-old_value = compute_the_old_way()\\n+new_value = compute_the_new_way()\\n***End Patch`
	db := writeHermesStore(t, hermes017Schema, `
		INSERT INTO messages (session_id,role,content,tool_call_id,tool_calls,tool_name,timestamp) VALUES
		 ('s','user','q',NULL,NULL,NULL,1785000000.0),
		 ('s','assistant','',NULL,'[{"id":"c1","type":"function","function":{"name":"patch","arguments":"{\"mode\": \"patch\", \"patch\": \"`+patch+`\"}"}}]',NULL,1785000001.0);`)
	by := hermesRoles(t, db)
	if got := strings.Join(by[RoleEdit], "|"); got != "/w/a.py\nold_value = compute_the_old_way()" {
		t.Errorf("edits = %q", by[RoleEdit])
	}
}

// A store with active but no compacted still hides a rewound turn.
func TestHermesActiveWithoutCompacted(t *testing.T) {
	db := writeHermesStore(t, `CREATE TABLE messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, role TEXT NOT NULL,
		content TEXT, tool_call_id TEXT, tool_calls TEXT, tool_name TEXT,
		timestamp REAL NOT NULL, active INTEGER NOT NULL DEFAULT 1);`, `
		INSERT INTO messages (session_id,role,content,timestamp,active) VALUES
		 ('s','user','keep this question',1785000000.0,1),
		 ('s','user','a turn taken back',1785000001.0,0);`)
	if got := strings.Join(hermesRoles(t, db)["user"], "|"); got != "keep this question" {
		t.Errorf("user = %q", got)
	}
}

// In-place compaction writes the kept head and tail again as live rows, with
// a summary between them; the archived originals are still there (#4296).
func TestHermesCompactionDoesNotRepeatProse(t *testing.T) {
	db := writeHermesStore(t, hermes017Schema, `
		INSERT INTO messages (session_id,role,content,tool_call_id,tool_calls,tool_name,timestamp,active,compacted) VALUES
		 ('s','user','fix the retry budget',NULL,NULL,NULL,1785000000.0,0,1),
		 ('s','assistant','looking at it now',NULL,NULL,NULL,1785000001.0,0,1),
		 ('s','user','fix the retry budget',NULL,NULL,NULL,1785000010.0,1,0),
		 ('s','assistant','[CONTEXT COMPACTION — REFERENCE ONLY] Earlier turns were compacted into the summary below.',NULL,NULL,NULL,1785000010.0,1,0),
		 ('s','assistant','looking at it now',NULL,NULL,NULL,1785000010.0,1,0),
		 ('s','user','now the pool size',NULL,NULL,NULL,1785000020.0,1,0);`)
	by := hermesRoles(t, db)
	if got := strings.Join(by["user"], "|"); got != "fix the retry budget|now the pool size" {
		t.Errorf("user = %q", by["user"])
	}
	if got := strings.Join(by["assistant"], "|"); got != "looking at it now" {
		t.Errorf("assistant = %q", by["assistant"])
	}
	if len(by[RoleSummary]) != 1 {
		t.Errorf("summary = %q, want the compaction summary under its own role", by[RoleSummary])
	}
}

// An archived row stands for one copy, in the batch compaction wrote around
// its summary. The same request and run after that batch is new work, even
// with the same text and the same derived call id (#4296).
func TestHermesRepeatAfterCompactionIsKept(t *testing.T) {
	call := `'[{"id":"call_3f1a2b4c5d6e","type":"function","function":{"name":"terminal","arguments":"{\"command\": \"go test ./...\"}"}}]'`
	db := writeHermesStore(t, hermes017Schema, `
		INSERT INTO messages (session_id,role,content,tool_call_id,tool_calls,tool_name,timestamp,active,compacted) VALUES
		 ('s','user','fix the pool',NULL,NULL,NULL,1785000000.0,0,1),
		 ('s','user','run the tests again',NULL,NULL,NULL,1785000001.0,0,1),
		 ('s','assistant','',NULL,`+call+`,NULL,1785000002.0,0,1),
		 ('s','tool','{"output": "FAIL pool_test.go:12", "exit_code": 1}','call_3f1a2b4c5d6e',NULL,'terminal',1785000003.0,0,1),
		 ('s','assistant','still failing',NULL,NULL,NULL,1785000004.0,0,1),
		 ('s','user','fix the pool',NULL,NULL,NULL,1785000010.0,1,0),
		 ('s','assistant','[CONTEXT COMPACTION — REFERENCE ONLY] Earlier turns were compacted into the summary below.',NULL,NULL,NULL,1785000010.0,1,0),
		 ('s','assistant','still failing',NULL,NULL,NULL,1785000010.0,1,0),
		 ('s','user','run the tests again',NULL,NULL,NULL,1785000020.0,1,0),
		 ('s','assistant','',NULL,`+call+`,NULL,1785000021.0,1,0),
		 ('s','tool','{"output": "ok  example/pool 0.2s", "exit_code": 0}','call_3f1a2b4c5d6e',NULL,'terminal',1785000022.0,1,0);`)
	by := hermesRoles(t, db)
	if got := strings.Join(by["user"], "|"); got != "fix the pool|run the tests again|run the tests again" {
		t.Errorf("user = %q", by["user"])
	}
	if got := strings.Join(by["assistant"], "|"); got != "still failing" {
		t.Errorf("assistant = %q", by["assistant"])
	}
	if got := strings.Join(by[RoleCommand], "|"); got != "$ go test ./...  → exit 1|$ go test ./...  → exit 0" {
		t.Errorf("commands = %q", by[RoleCommand])
	}
	if got := strings.Join(by[RoleToolOutput], "|"); got != "FAIL pool_test.go:12|ok  example/pool 0.2s" {
		t.Errorf("tool output = %q", by[RoleToolOutput])
	}
}

// When the kept head ends on assistant and the tail starts on user, Hermes
// prepends the summary to the first tail message, up to an end marker
// (agent/context_compressor.py _merge_summary_into_tail). What follows the
// marker is that message, and may be the only copy of the prompt.
func TestHermesSummaryMergedIntoPrompt(t *testing.T) {
	db := writeHermesStore(t, hermes017Schema, `
		INSERT INTO messages (session_id,role,content,tool_call_id,tool_calls,tool_name,timestamp,active,compacted) VALUES
		 ('s','user','fix the pool',NULL,NULL,NULL,1785000000.0,0,1),
		 ('s','assistant','looking at it now',NULL,NULL,NULL,1785000001.0,0,1),
		 ('s','user','fix the pool',NULL,NULL,NULL,1785000010.0,1,0),
		 ('s','assistant','looking at it now',NULL,NULL,NULL,1785000010.0,1,0),
		 ('s','user','[CONTEXT COMPACTION — REFERENCE ONLY] Earlier turns were compacted into the summary below.
## Active Task
fix the pool

--- END OF CONTEXT SUMMARY — respond to the message below, not the summary above ---

now raise the pool size to 32',NULL,NULL,NULL,1785000010.0,1,0);`)
	by := hermesRoles(t, db)
	if got := strings.Join(by["user"], "|"); got != "fix the pool|now raise the pool size to 32" {
		t.Errorf("user = %q", by["user"])
	}
	if got := strings.Join(by[RoleSummary], "|"); len(by[RoleSummary]) != 1 || strings.Contains(got, "pool size to 32") {
		t.Errorf("summary = %q, want the summary alone", by[RoleSummary])
	}
}

// A quiet passing run has an empty output; what sits beside it is
// bookkeeping, not the result.
func TestHermesEmptyOutputIsNoResult(t *testing.T) {
	db := writeHermesStore(t, hermes017Schema, `
		INSERT INTO messages (session_id,role,content,tool_call_id,tool_calls,tool_name,timestamp) VALUES
		 ('s','user','run the tests',NULL,NULL,NULL,1785000000.0),
		 ('s','tool','{"output": "", "exit_code": 0, "error": null, "verification_evidence": {"command": "go test ./...", "kind": "test", "scope": "repo", "status": "passed"}}','c1',NULL,'terminal',1785000001.0);`)
	if got := hermesRoles(t, db)[RoleToolOutput]; len(got) != 0 {
		t.Errorf("tool output = %q, want none", got)
	}
}

// Older Hermes opened its summary with LEGACY_SUMMARY_PREFIX.
func TestHermesLegacySummaryPrefix(t *testing.T) {
	db := writeHermesStore(t, hermes017Schema, `
		INSERT INTO messages (session_id,role,content,timestamp) VALUES
		 ('s','user','fix the pool',1785000000.0),
		 ('s','assistant','[CONTEXT SUMMARY]: the pool was resized to 16',1785000001.0);`)
	by := hermesRoles(t, db)
	if len(by["assistant"]) != 0 || len(by[RoleSummary]) != 1 {
		t.Errorf("assistant = %q, summary = %q", by["assistant"], by[RoleSummary])
	}
}

// A second compaction archives the first one's batch — its head copy,
// summary and tail copy — so every summary is an anchor, archived or not.
func TestHermesTwoCompactions(t *testing.T) {
	db := writeHermesStore(t, hermes017Schema, `
		INSERT INTO messages (session_id,role,content,timestamp,active,compacted) VALUES
		 ('s','user','set up the repo',10.0,0,1),
		 ('s','assistant','done',11.0,0,1),
		 ('s','user','add the billing tables',12.0,0,1),
		 ('s','assistant','added',13.0,0,1),
		 ('s','user','set up the repo',20.0,0,1),
		 ('s','assistant','[CONTEXT COMPACTION — REFERENCE ONLY] first summary',20.000001,0,1),
		 ('s','user','add the billing tables',20.000002,0,1),
		 ('s','assistant','added',20.000003,0,1),
		 ('s','user','now the invoices',30.0,0,1),
		 ('s','assistant','invoices done',31.0,0,1),
		 ('s','user','set up the repo',40.0,1,0),
		 ('s','assistant','[CONTEXT COMPACTION — REFERENCE ONLY] second summary',40.000001,1,0),
		 ('s','user','now the invoices',40.000002,1,0),
		 ('s','assistant','invoices done',40.000003,1,0);`)
	by := hermesRoles(t, db)
	if got := strings.Join(by["user"], "|"); got != "set up the repo|add the billing tables|now the invoices" {
		t.Errorf("user = %q", by["user"])
	}
	if got := strings.Join(by["assistant"], "|"); got != "done|added|invoices done" {
		t.Errorf("assistant = %q", by["assistant"])
	}
	if len(by[RoleSummary]) != 2 {
		t.Errorf("summary = %q", by[RoleSummary])
	}
}

// Compaction writes its copies in one transaction, at the time of the
// summary or under their original timestamp (hermes_state.py
// _insert_message_rows). A turn typed minutes later is new, however much it
// looks like the archive's end.
func TestHermesTurnAfterCompactionLikeTheArchiveEnd(t *testing.T) {
	db := writeHermesStore(t, hermes017Schema, `
		INSERT INTO messages (session_id,role,content,timestamp,active,compacted) VALUES
		 ('s','user','start the migration',1.0,0,1),
		 ('s','assistant','sure',2.0,0,1),
		 ('s','user','continue',3.0,0,1),
		 ('s','assistant','ok',4.0,0,1),
		 ('s','user','continue',5.0,0,1),
		 ('s','assistant','ok',6.0,0,1),
		 ('s','user','start the migration',100.0,1,0),
		 ('s','assistant','[CONTEXT COMPACTION — REFERENCE ONLY] summary',100.0,1,0),
		 ('s','user','continue',100.000001,1,0),
		 ('s','assistant','ok',100.000002,1,0),
		 ('s','user','continue',400.0,1,0),
		 ('s','assistant','ok',401.0,1,0);`)
	by := hermesRoles(t, db)
	if got := strings.Count(strings.Join(by["user"], "|"), "continue"); got != 3 {
		t.Errorf("user = %q, want continue three times", by["user"])
	}
}

// An empty tail, then a rerun that repeats the archive's last exchange with
// the same derived call id: a result that is neither the archived one nor a
// stub is no copy.
func TestHermesRerunAfterEmptyTail(t *testing.T) {
	call := `'[{"id":"call_3f1a2b4c5d6e","type":"function","function":{"name":"terminal","arguments":"{\"command\": \"go test ./...\"}"}}]'`
	db := writeHermesStore(t, hermes017Schema, `
		INSERT INTO messages (session_id,role,content,tool_call_id,tool_calls,tool_name,timestamp,active,compacted) VALUES
		 ('s','user','make the tests pass',NULL,NULL,NULL,1.0,0,1),
		 ('s','assistant','',NULL,`+call+`,NULL,2.0,0,1),
		 ('s','tool','{"output": "FAIL pool_test.go:12", "exit_code": 1}','call_3f1a2b4c5d6e',NULL,'terminal',3.0,0,1),
		 ('s','user','make the tests pass',NULL,NULL,NULL,100.0,1,0),
		 ('s','assistant','[CONTEXT COMPACTION — REFERENCE ONLY] summary',NULL,NULL,NULL,100.0,1,0),
		 ('s','assistant','',NULL,`+call+`,NULL,200.0,1,0),
		 ('s','tool','{"output": "ok  example/pool 0.2s", "exit_code": 0}','call_3f1a2b4c5d6e',NULL,'terminal',201.0,1,0);`)
	by := hermesRoles(t, db)
	if got := strings.Join(by[RoleCommand], "|"); got != "$ go test ./...  → exit 1|$ go test ./...  → exit 0" {
		t.Errorf("commands = %q", by[RoleCommand])
	}
	if got := strings.Join(by[RoleToolOutput], "|"); got != "FAIL pool_test.go:12|ok  example/pool 0.2s" {
		t.Errorf("tool output = %q", by[RoleToolOutput])
	}
}

// Compaction replaces an image in a kept message with a text part
// (agent/context_compressor.py _strip_historical_media); the copy is still a
// copy, and the placeholder says nothing.
func TestHermesCompactionStrippedImage(t *testing.T) {
	orig := `[{"type": "text", "text": "why is this chart flat"}, {"type": "image_url", "image_url": {"url": "data:image/png;base64,iVBORw0KGgo"}}]`
	stripped := `[{"type": "text", "text": "why is this chart flat"}, {"type": "text", "text": "[Attached image — stripped after compression]"}]`
	db := writeHermesStore(t, hermes017Schema, `
		INSERT INTO messages (session_id,role,content,timestamp,active,compacted) VALUES
		 ('s','user',char(0)||'json:'||'`+orig+`',1.0,0,1),
		 ('s','assistant','it is clipped at 100',2.0,0,1),
		 ('s','user',char(0)||'json:'||'`+stripped+`',100.0,1,0),
		 ('s','assistant','[CONTEXT COMPACTION — REFERENCE ONLY] summary',100.0,1,0);`)
	if got := strings.Join(hermesRoles(t, db)["user"], "|"); got != "why is this chart flat" {
		t.Errorf("user = %q", got)
	}
}

// The Postgres path reads in insertion order like the SQLite one, which is
// the order Hermes reads its own sessions in.
func TestHermesPGReadsInInsertionOrder(t *testing.T) {
	var sql string
	defer SetHermesPGRunner(func(_, q string) ([]byte, error) {
		if strings.Contains(q, "from messages") {
			sql = q
		}
		return []byte("[]"), nil
	})()
	if _, err := ParseHermesPG("postgres://deja@192.0.2.1/hermes", 0); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, "order by session_id,id)") {
		t.Errorf("query = %q, want insertion order", sql)
	}
}
