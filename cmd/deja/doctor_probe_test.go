package main

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// doctor asks one question of a store — does it parse into sessions — and for
// opencode it was answering by reading the whole database: 6.5 seconds of an
// 8 second report on a 2.8 GB store.
func TestOpencodeProbeReadsOnlyTheNewestSession(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	sql := `
CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT, time_created INTEGER, time_updated INTEGER);
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, data TEXT, time_created INTEGER);
CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, data TEXT);
INSERT INTO session VALUES ('old','/w',1000,1000);
INSERT INTO session VALUES ('new','/w',2000,2000);
INSERT INTO message VALUES ('m1','old','{"role":"user","time":{"created":1000}}',1000);
INSERT INTO message VALUES ('m2','new','{"role":"user","time":{"created":2000}}',2000);
INSERT INTO part VALUES ('p1','m1','{"type":"text","text":"OLDSESSION"}');
INSERT INTO part VALUES ('p2','m2','{"type":"text","text":"NEWSESSION"}');
`
	if out, err := exec.Command("sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	ss, err := doctorProbeOpencode(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "new" {
		t.Fatalf("probe returned %d sessions, want just the newest: %+v", len(ss), ss)
	}
	// The point of the check survives: a store that parses reports sessions.
	if len(ss[0].Messages) == 0 {
		t.Fatal("probe returned a session with no messages, doctor would call the store broken")
	}
}
