package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func seedDB(t *testing.T, name, schema string) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), name)
	if out, err := exec.Command("sqlite3", db, schema).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	return db
}

// The incremental path asks each SQLite-backed harness for what changed since
// a timestamp. Getting that filter wrong either reindexes everything on every
// pass or silently misses new messages.
func TestGooseSinceFilterBoundsWhatItReturns(t *testing.T) {
	db := seedDB(t, "sessions.db", `
CREATE TABLE sessions (id TEXT PRIMARY KEY, name TEXT, description TEXT, working_dir TEXT, created_at TEXT, updated_at TEXT, session_type TEXT);
CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT, role TEXT, content_json TEXT, created_timestamp INTEGER);
INSERT INTO sessions VALUES ('s1','n','desc','/w','2026-01-01T00:00:00Z','2026-01-01T00:00:10Z','user');
INSERT INTO messages VALUES (1,'s1','user','[{"type":"text","text":"OLDMESSAGE"}]',1767225600);
INSERT INTO messages VALUES (2,'s1','user','[{"type":"text","text":"NEWMESSAGE"}]',1798761600);
`)
	all, err := ParseGooseDBSince(db, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || len(all[0].Messages) != 2 {
		t.Fatalf("zero time should return everything, got %+v", all)
	}
	cut := time.Unix(1790000000, 0)
	since, err := ParseGooseDBSince(db, cut)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range since {
		for _, m := range s.Messages {
			if m.Text == "OLDMESSAGE" {
				t.Fatalf("since filter returned a message from before the cut: %+v", s.Messages)
			}
		}
	}
}

// doctor asks one question of the opencode store, and answering it by reading
// a multi-gigabyte database cost 6.5 seconds of an 8 second report.
func TestOpencodeNewestReadsOneSession(t *testing.T) {
	db := seedDB(t, "opencode.db", `
CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT, time_created INTEGER, time_updated INTEGER);
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, data TEXT, time_created INTEGER);
CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, data TEXT);
INSERT INTO session VALUES ('old','/w',1000,1000);
INSERT INTO session VALUES ('new','/w',2000,2000);
INSERT INTO message VALUES ('m1','old','{"role":"user","time":{"created":1000}}',1000);
INSERT INTO message VALUES ('m2','new','{"role":"user","time":{"created":2000}}',2000);
INSERT INTO part VALUES ('p1','m1','{"type":"text","text":"OLDSESSION"}');
INSERT INTO part VALUES ('p2','m2','{"type":"text","text":"NEWSESSION"}');
`)
	ss, err := ParseOpencodeNewest(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "new" {
		t.Fatalf("got %+v, want only the newest session", ss)
	}
	if len(ss[0].Messages) == 0 {
		t.Fatal("newest session came back empty; doctor would call the store broken")
	}
	// A store that is not there answers with nothing rather than an error,
	// and must not be created by the attempt.
	if ss, err := ParseOpencodeNewest(filepath.Join(t.TempDir(), "absent.db")); err != nil || ss != nil {
		t.Fatalf("absent store: %v %v", ss, err)
	}
}

// KindForPath decides which parser owns a file, and two harnesses writing the
// same filename is how roo history ended up filed under cline.
func TestKindForPathAttributesFilesToTheirHarness(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_ROO_ROOTS", "")
	t.Setenv("DEJA_CLINE_ROOTS", "")
	cli := filepath.Join(home, ".vscode-mock", "global-storage")
	t.Setenv("DEJA_ROO_CLI_ROOT", cli)
	// Roots are only considered when they exist: a path under a store the
	// user does not have belongs to nobody.
	if err := os.MkdirAll(filepath.Join(cli, "tasks", "1"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(home, "claude"))

	for path, want := range map[string]string{
		filepath.Join(cli, "tasks", "1", "api_conversation_history.json"): "roo",
		filepath.Join(home, "claude", "project", "s.jsonl"):               "claude",
		filepath.Join(home, "nothing", "random.txt"):                      "",
	} {
		if got := KindForPath(path); got != want {
			t.Fatalf("KindForPath(%s) = %q, want %q", path, got, want)
		}
	}
}

// Timestamps arrive in whatever shape a harness happens to write. An
// unparseable one must degrade to "no time" rather than to a wrong time: a
// session dated 0001-01-01 sorts before everything and never surfaces.
func TestGrokDBTimeAcceptsTheShapesItSees(t *testing.T) {
	for in, wantZero := range map[string]bool{
		"2026-07-27T15:06:24.715Z": false,
		"2026-07-27T15:06:24Z":     false,
		"2026-07-27 15:06:24":      false,
		"":                         true,
		"not a time":               true,
	} {
		got := grokDBTime(in)
		if got.IsZero() != wantZero {
			t.Fatalf("grokDBTime(%q) = %v, zero=%v want zero=%v", in, got, got.IsZero(), wantZero)
		}
	}
}

// Message bodies differ per role: a user turn carries a plain string, an
// assistant turn an array of typed blocks. Reading only one shape loses half
// the conversation.
func TestGrokMessageTextHandlesBothShapes(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"plain string": {`{"role":"user","content":"hello there"}`, "hello there"},
		"text blocks":  {`{"role":"assistant","content":[{"type":"text","text":"first"},{"type":"text","text":"second"}]}`, "first\nsecond"},
		"tool only":    {`{"role":"assistant","content":[{"type":"tool_use","id":"t1"}]}`, ""},
		"garbage":      {`not json`, ""},
	} {
		if got := grokMessageText(tc.body); got != tc.want {
			t.Fatalf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}
