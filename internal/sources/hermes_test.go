package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	cmd := exec.Command("sqlite3", db)
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
	if first.Title == "" {
		t.Fatal("title not derived from the first message")
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
	// The root store has no profile directory to be named after.
	var names []string
	for _, s := range ss {
		names = append(names, s.Project)
	}
	if !contains(names, "hermes") || !contains(names, "architect") {
		t.Fatalf("projects = %v", names)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
