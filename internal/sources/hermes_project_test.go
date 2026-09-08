package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
	cmd := exec.Command("sqlite3", db)
	cmd.Stdin = strings.NewReader(`CREATE TABLE messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		role TEXT NOT NULL,
		content TEXT,
		timestamp REAL NOT NULL);
		CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT NOT NULL, started_at REAL NOT NULL, cwd TEXT, title TEXT);
		INSERT INTO sessions (id,source,started_at,cwd) VALUES
		 ('routed','cli',1785000000.0,'/home/qa/work/routepilot'),
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
	want := map[string]string{"routed": "work/routepilot", "nowhere": "writer", "blank": "writer", "orphan": "writer"}
	for id, p := range want {
		if got[id] != p {
			t.Errorf("session %s project = %q, want %q", id, got[id], p)
		}
	}
}
