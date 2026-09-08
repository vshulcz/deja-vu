package index

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

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
	cmd := exec.Command("sqlite3", db)
	cmd.Stdin = strings.NewReader(`CREATE TABLE messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		role TEXT NOT NULL,
		content TEXT,
		timestamp REAL NOT NULL);
		INSERT INTO messages (session_id,role,content,timestamp) VALUES
		 ('hi','user','hi',1785234496.0),
		 ('hi','assistant','Hi! What can I help with?',1785234500.0),
		 ('hi','user','hiddify configs: the routepilot import',1785234520.0),
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
		"hi":    "hiddify configs: the routepilot import",
		"greet": "why does the ptarmigan cache miss on restart",
		"bot":   "nobody typed here",
	}
	for _, s := range ss {
		if got := metaForSession(s).Title; got != want[s.ID] {
			t.Errorf("session %s titled %q, want %q", s.ID, got, want[s.ID])
		}
	}
}
