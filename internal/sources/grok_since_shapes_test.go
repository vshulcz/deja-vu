package sources

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// grokShapeDB writes the same two messages with whichever stamp shape the
// caller names — grokDBTime reads all of them, so the filter has to as well.
func grokShapeDB(t *testing.T, early, late string) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "grok.db")
	schema := `
CREATE TABLE workspaces (id TEXT PRIMARY KEY, canonical_path TEXT);
CREATE TABLE sessions (id TEXT PRIMARY KEY, workspace_id TEXT, title TEXT, cwd_last TEXT, created_at TEXT);
CREATE TABLE messages (session_id TEXT, seq INTEGER, role TEXT, message_json TEXT, created_at TEXT);
INSERT INTO sessions VALUES ('s1','w1','Pool timeouts','/work/api','` + early + `');
INSERT INTO messages VALUES ('s1',0,'user','{"role":"user","content":"connection pool exhausted again"}','` + early + `');
INSERT INTO messages VALUES ('s1',1,'assistant','{"role":"assistant","content":[{"type":"text","text":"raise max_conns"}]}','` + late + `');
`
	cmd := exec.Command("sqlite3", db)
	cmd.Stdin = stringsReader(schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	return db
}

// The filter compared strings while the reader parsed three shapes, so a store
// writing sqlite's own "2026-07-27 15:29:47" had every same-day message read
// as older than the watermark: a space sorts below the T of an RFC3339 string.
// Nothing reached the index until the date rolled over (#2150, the shape #2030
// fixed for goose).
func TestGrokSinceHandlesEveryStampShapeItReads(t *testing.T) {
	for _, shape := range []struct {
		name, early, late string
	}{
		{"rfc3339", "2026-07-27T10:00:00.000Z", "2026-07-27T15:29:47.000Z"},
		{"sqlite", "2026-07-27 10:00:00", "2026-07-27 15:29:47"},
		// A stamp carrying an offset rather than Z: normalising both sides
		// converts it, where a text comparison reads the digits as written.
		{"offset", "2026-07-27T13:00:00+03:00", "2026-07-27T18:29:47+03:00"},
	} {
		t.Run(shape.name, func(t *testing.T) {
			db := grokShapeDB(t, shape.early, shape.late)
			count := func(cut string) int {
				at, err := time.Parse(time.RFC3339, cut)
				if err != nil {
					t.Fatal(err)
				}
				ss, err := ParseGrokDBSince(db, at)
				if err != nil {
					t.Fatal(err)
				}
				n := 0
				for _, s := range ss {
					n += len(s.Messages)
				}
				return n
			}
			// The premise: both messages are there when nothing is filtered.
			ss, err := ParseGrokDBSince(db, time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			if len(ss) != 1 || len(ss[0].Messages) != 2 {
				t.Fatalf("unfiltered read: %d session(s), want one with two messages", len(ss))
			}
			if n := count("2026-07-27T09:00:00Z"); n != 2 {
				t.Errorf("a watermark earlier the same day returned %d messages, want both", n)
			}
			if n := count("2026-07-27T12:00:00Z"); n != 1 {
				t.Errorf("a watermark between the two returned %d messages, want the later one", n)
			}
			if n := count("2026-07-27T16:00:00Z"); n != 0 {
				t.Errorf("a watermark after both returned %d messages, want none", n)
			}
			// The boundary: a message stamped at the watermark is already in
			// the index, and one a fraction of a second later is not.
			if n := count("2026-07-27T15:29:47Z"); n != 0 {
				t.Errorf("a watermark exactly at the later message returned %d, want none", n)
			}
		})
	}
}

// A stamp sqlite cannot read normalises to null. Such a row comes back on
// every incremental pass rather than disappearing from all of them: an
// unreadable stamp is a reason to look at a message, not to hide it — the rule
// zed's reader already follows.
func TestGrokSinceKeepsAMessageWithAnUnreadableStamp(t *testing.T) {
	db := grokShapeDB(t, "2026-07-27 10:00:00", "not a timestamp at all")
	at, err := time.Parse(time.RFC3339, "2026-07-27T16:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGrokDBSince(db, at)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range ss {
		n += len(s.Messages)
	}
	if n != 1 {
		t.Errorf("%d message(s) came back, want the one whose stamp cannot be read", n)
	}
}
