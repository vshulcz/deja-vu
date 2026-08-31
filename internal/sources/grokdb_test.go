package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func grokTestDB(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "grok.db")
	schema := `
CREATE TABLE workspaces (id TEXT PRIMARY KEY, canonical_path TEXT);
CREATE TABLE sessions (id TEXT PRIMARY KEY, workspace_id TEXT, title TEXT, cwd_last TEXT, created_at TEXT);
CREATE TABLE messages (session_id TEXT, seq INTEGER, role TEXT, message_json TEXT, created_at TEXT);
INSERT INTO sessions VALUES ('s1','w1','Pool timeouts','/work/api','2026-07-27T10:00:00.000Z');
INSERT INTO messages VALUES ('s1',0,'user','{"role":"user","content":"connection pool exhausted again"}','2026-07-27T10:00:00.000Z');
INSERT INTO messages VALUES ('s1',1,'assistant','{"role":"assistant","content":[{"type":"text","text":"raise max_conns"},{"type":"tool_use","id":"t1"}]}','2026-07-27T10:00:05.000Z');
INSERT INTO messages VALUES ('s1',2,'tool','{"role":"tool","content":"ignored"}','2026-07-27T10:00:06.000Z');
INSERT INTO messages VALUES ('s1',3,'assistant','{"role":"assistant","content":[{"type":"tool_use","id":"t2"}]}','2026-07-27T10:00:07.000Z');
`
	cmd := exec.Command("sqlite3", db)
	cmd.Stdin = stringsReader(schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	return db
}

func stringsReader(s string) *os.File {
	r, w, _ := os.Pipe()
	go func() {
		_, _ = w.WriteString(s)
		_ = w.Close()
	}()
	return r
}

func TestParseGrokDB(t *testing.T) {
	ss, err := ParseGrokDBSince(grokTestDB(t), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	s := ss[0]
	// Project is the directory's short name, not the whole cwd path: trust
	// policy and per-project scoping key on the same name the file-based grok
	// parser produces (projectName), so /work/api and its sibling must both
	// scope as "api".
	if s.Harness != "grok" || s.Project != "api" || s.Title != "Pool timeouts" {
		t.Fatalf("session metadata wrong: %+v", s)
	}
	// A user message carries a plain string, an assistant message an array of
	// blocks; tool traffic and block-less turns are not history worth indexing.
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %d, want the user turn and the one with text: %+v", len(s.Messages), s.Messages)
	}
	if s.Messages[0].Text != "connection pool exhausted again" {
		t.Fatalf("user text = %q", s.Messages[0].Text)
	}
	if s.Messages[1].Text != "raise max_conns" {
		t.Fatalf("assistant text = %q, block extraction is wrong", s.Messages[1].Text)
	}
	if s.Started.IsZero() || s.Updated.Before(s.Started) {
		t.Fatalf("times wrong: started=%v updated=%v", s.Started, s.Updated)
	}
}

// The incremental path reindexes only what changed; a since filter that takes
// everything would rebuild the whole store on every write.
func TestParseGrokDBSinceFilters(t *testing.T) {
	db := grokTestDB(t)
	cut, err := time.Parse(time.RFC3339, "2026-07-27T10:00:02Z")
	if err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGrokDBSince(db, cut)
	if err != nil {
		t.Fatal(err)
	}
	// The session it picks comes back whole — a partial return replaces what
	// the index holds for that session and would take the earlier turns with
	// it (#2075) — so what this asserts is that the new message is there.
	// Asserting nothing about it passed for months while the filter returned
	// nothing at all, because a missing quote made sqlite reject the query and
	// the error was swallowed as "no sessions".
	var sawNew bool
	for _, s := range ss {
		for _, m := range s.Messages {
			if m.Text == "raise max_conns" {
				sawNew = true
			}
		}
	}
	if !sawNew {
		t.Fatal("since filter returned nothing; incremental indexing would never pick up new messages")
	}
}

func TestParseGrokDBIgnoresMissingStore(t *testing.T) {
	ss, err := ParseGrokDBSince(filepath.Join(t.TempDir(), "absent.db"), time.Time{})
	if err != nil || ss != nil {
		t.Fatalf("missing store: sessions=%v err=%v", ss, err)
	}
	// sqlite3 creates a database on open; a parser that let it would leave a
	// file behind and then index it forever.
	if _, err := os.Stat(filepath.Join(t.TempDir(), "absent.db")); !os.IsNotExist(err) {
		t.Fatal("parser created the store it was asked to read")
	}
}
