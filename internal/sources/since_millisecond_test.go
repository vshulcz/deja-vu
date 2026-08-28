package sources

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The since clauses normalise both sides with strftime's %f, which is
// milliseconds, while the readers parse RFC3339Nano. A message stamped inside
// the watermark's own millisecond compared as not-after it and was skipped for
// good — and the two sides do not even agree on which millisecond a stamp is
// in, because Go truncates where strftime rounds (#2155).
func TestAMessageInsideTheWatermarksMillisecondIsNotLost(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "grok.db")
	schema := `
CREATE TABLE workspaces (id TEXT PRIMARY KEY, canonical_path TEXT);
CREATE TABLE sessions (id TEXT PRIMARY KEY, workspace_id TEXT, title TEXT, cwd_last TEXT, created_at TEXT);
CREATE TABLE messages (session_id TEXT, seq INTEGER, role TEXT, message_json TEXT, created_at TEXT);
INSERT INTO sessions VALUES ('s1','w1','t','/work/api','2026-07-27T10:00:00.0000000Z');
INSERT INTO messages VALUES ('s1',0,'user','{"role":"user","content":"first message"}','2026-07-27T10:00:00.8121000Z');
INSERT INTO messages VALUES ('s1',1,'user','{"role":"user","content":"second message"}','2026-07-27T10:00:00.8129000Z');
`
	cmd := exec.Command("sqlite3", db)
	cmd.Stdin = stringsReader(schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	read := func(cut string) []string {
		at, err := time.Parse(time.RFC3339Nano, cut)
		if err != nil {
			t.Fatal(err)
		}
		ss, err := ParseGrokDBSince(db, at)
		if err != nil {
			t.Fatal(err)
		}
		var texts []string
		for _, s := range ss {
			for _, m := range s.Messages {
				texts = append(texts, m.Text)
			}
		}
		return texts
	}
	// A watermark before both messages must leave neither behind.
	if got := read("2026-07-27T10:00:00.8120000Z"); len(got) != 2 {
		t.Errorf("watermark before both returned %v, want both messages", got)
	}
	// One stamped between them keeps at least the later one — the earlier may
	// come back too, which costs a message being offered twice and is the side
	// of the error worth being on.
	if got := read("2026-07-27T10:00:00.8125000Z"); !strings.Contains(strings.Join(got, " "), "second message") {
		t.Errorf("watermark between the two returned %v, want at least the later message", got)
	}
	// And it is still a filter: a watermark a second later returns nothing.
	if got := read("2026-07-27T10:00:01.0000000Z"); len(got) != 0 {
		t.Errorf("watermark after both returned %v, want none", got)
	}
}
