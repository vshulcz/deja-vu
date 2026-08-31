package index

import (
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// runStoreSQL feeds a script to the sqlite3 CLI on stdin: a fixture that opens
// with a `--` comment would otherwise be read as an option.
func runStoreSQL(t *testing.T, db, sql string) {
	t.Helper()
	cmd := exec.Command("sqlite3", db)
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
			runStoreSQL(t, db, `INSERT INTO messages VALUES ('gl',2,'user','{"content":"needlelatest the follow up"}','2026-07-27T13:00:00.000Z');
INSERT INTO sessions VALUES ('gn','w','Brand new','/work/api','2026-07-27T14:00:00.000Z');
INSERT INTO messages VALUES ('gn',0,'user','{"content":"needlearrival a session that did not exist"}','2026-07-27T14:00:00.000Z');`)
		},
		key: "grok:gl", turns: 3,
	}, {
		harness: "zed",
		seed: func(t *testing.T, tmp string) string {
			db := filepath.Join(tmp, "threads.db")
			t.Setenv("DEJA_ZED_DB", db)
			runStoreSQL(t, db, `create table threads (id text primary key, summary text not null, updated_at text not null, data_type text not null, data blob not null, parent_id text, folder_paths text, folder_paths_order text, created_at text);
`+zedJSONThreadRow("zq", "2026-07-26T10:00:00+00:00", "needlequiet a session nobody touches again")+
				zedJSONThreadRow("zl", "2026-07-27T11:00:00+00:00", "needleopening the opening question", "needlemiddle the first answer"))
			return db
		},
		grow: func(t *testing.T, db string) {
			runStoreSQL(t, db, zedJSONThreadRow("zl", "2026-07-27T13:00:00+00:00",
				"needleopening the opening question", "needlemiddle the first answer", "needlelatest the follow up")+
				zedJSONThreadRow("zn", "2026-07-27T14:00:00+00:00", "needlearrival a session that did not exist"))
		},
		key: "zed:zl", turns: 3,
	}, {
		harness: "hermes",
		seed: func(t *testing.T, tmp string) string {
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
			if err := Ensure(dir, "", false, nil); err != nil {
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
