package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hermesStoreWithSessions writes a store carrying both tables — the shape a
// current Hermes writes. The helper beside this one predates the sessions
// table, and a fixture without it is what let the cwd go unread.
func hermesStoreWithSessions(t *testing.T, dir, rows string) string {
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
		session_id TEXT NOT NULL, role TEXT NOT NULL, content TEXT,
		tool_call_id TEXT, tool_calls TEXT, tool_name TEXT,
		timestamp REAL NOT NULL, token_count INTEGER, finish_reason TEXT);
	CREATE TABLE sessions (
		id TEXT PRIMARY KEY, cwd TEXT, git_repo_root TEXT, title TEXT);` + rows
	cmd := exec.Command("sqlite3", db)
	cmd.Stdin = strings.NewReader(schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	return db
}

// Every Hermes session was stamped with the profile directory while the store
// says where the person was working, so the per-prompt hook — which ranks the
// payload's project — never served a Hermes session to the project it was
// about (#3257).
func TestHermesSessionsLandInTheProjectTheyWereWorkedIn(t *testing.T) {
	db := hermesStoreWithSessions(t, filepath.Join(t.TempDir(), "architect"), `
INSERT INTO sessions VALUES ('s1','/Users/me/coding/widgetd',NULL,NULL);
INSERT INTO sessions VALUES ('s2',NULL,NULL,NULL);
INSERT INTO messages (session_id,role,content,timestamp) VALUES ('s1','user','why does the vantrell import drop configs',1785000000.5);
INSERT INTO messages (session_id,role,content,timestamp) VALUES ('s1','assistant','the parser skips a null host',1785000001);
INSERT INTO messages (session_id,role,content,timestamp) VALUES ('s2','user','and the unrelated one',1785000002);
`)
	ss, err := ParseHermesDB(db)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range ss {
		got[s.ID] = s.Project
	}
	// The same name a Cline or Roo session from that workspace gets — two
	// segments, so two projects called "api" under different parents stay
	// apart — which is what makes the hook's project scoping match.
	want := claudeProjectName(pathToProjectKey("/Users/me/coding/widgetd"))
	if want == "" || want == "hermes" || want == "architect" {
		t.Fatalf("the shared naming gave %q, so this test proves nothing", want)
	}
	if got["s1"] != want {
		t.Errorf("s1 project = %q, want %q — the directory it was worked in", got["s1"], want)
	}
	// A session the table does not place keeps the profile, which is the only
	// grouping such a row has.
	if got["s2"] != "architect" {
		t.Errorf("s2 project = %q, want the profile name", got["s2"])
	}
}

// An older store has no sessions table at all, and that must cost the grouping
// rather than the harness.
func TestHermesWithoutASessionsTableStillParses(t *testing.T) {
	db := writeHermesDB(t, filepath.Join(t.TempDir(), "architect"), `
INSERT INTO messages (session_id,role,content,timestamp) VALUES ('s1','user','the old store still reads',1785000000);
`)
	ss, err := ParseHermesDB(db)
	if err != nil {
		t.Fatalf("an older store failed to parse: %v", err)
	}
	if len(ss) != 1 || ss[0].Project != "architect" {
		t.Fatalf("sessions = %#v", ss)
	}
}
