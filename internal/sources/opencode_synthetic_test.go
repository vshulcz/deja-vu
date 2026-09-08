package sources

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// opencode marks the text it writes under the user role itself with
// synthetic: true — "Continue if you have next steps…", "The following tool
// was executed by the user" — and 338 of them indexed as the person's words on
// a real store (#3299).
func TestOpencodeSyntheticPartsAreNotThePersons(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"user","agent":"build"}');
insert into part values('p1','m1','{"type":"text","text":"why does the retry loop drop the last attempt","time":{"start":"2026-01-02T03:00:00Z"}}');
insert into message values('m2','s1',1767409260000,'{"role":"assistant","agent":"build"}');
insert into part values('p2','m2','{"type":"text","text":"the deadline is reused","time":{"start":"2026-01-02T03:01:00Z"}}');
insert into message values('m3','s1',1767409320000,'{"role":"user","agent":"build"}');
insert into part values('p3','m3','{"type":"text","synthetic":true,"text":"Continue if you have next steps, or stop and ask for clarification if you are unsure how to proceed.","time":{"start":"2026-01-02T03:02:00Z"}}');
insert into message values('m4','s1',1767409380000,'{"role":"user","agent":"build"}');
insert into part values('p4','m4','{"type":"text","synthetic":true,"text":"The following tool was executed by the user","time":{"start":"2026-01-02T03:03:00Z"}}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	var users []string
	for _, m := range ss[0].Messages {
		if m.Role == "user" {
			users = append(users, m.Text)
		}
	}
	if len(users) != 1 || users[0] != "why does the retry loop drop the last attempt" {
		t.Errorf("user turns = %q, want the typed prompt alone", users)
	}
}
