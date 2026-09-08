package sources

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// opencode records a subagent run as its own session with parent_id pointing
// at the spawner — 922 of 1472 sessions on a real store — and the reader never
// read the column, so every child listed as a person's own session (#3301).
func TestOpencodeSubagentSessionKnowsItsParent(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text, parent_id text, directory text, title text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('ses_parent',NULL,'/w','Adversarial analysis','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into session values('ses_child','ses_parent','/w','Review QA screenshots (@general subagent)','2026-01-02T03:10:00Z','2026-01-02T03:20:00Z');
insert into message values('m1','ses_parent',1767409200000,'{"role":"user","agent":"build"}');
insert into part values('p1','m1','{"type":"text","text":"review the screenshots","time":{"start":"2026-01-02T03:00:00Z"}}');
insert into message values('m2','ses_child',1767409800000,'{"role":"user","agent":"general"}');
insert into part values('p2','m2','{"type":"text","text":"Review QA screenshots in /w/shots and list the defects","time":{"start":"2026-01-02T03:10:00Z"}}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 2 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	for _, s := range ss {
		switch s.ID {
		case "ses_child":
			if s.Kind != "subagent" || s.Parent != "ses_parent" {
				t.Errorf("child kind=%q parent=%q, want subagent / ses_parent", s.Kind, s.Parent)
			}
		case "ses_parent":
			if s.Kind != "" || s.Parent != "" {
				t.Errorf("parent kind=%q parent=%q, want none", s.Kind, s.Parent)
			}
		}
	}
}
