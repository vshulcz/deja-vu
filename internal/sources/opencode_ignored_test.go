package sources

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// A part opencode marks `ignored` is one it does not feed to the model. A
// compression plugin's status line carries it and is filed under the user role,
// so those banners were indexed as the person's words — 376 of them on a real
// store, 20.8 KB, and the flag was exact there: every ignored part was one of
// these, and none of the 4,420 real user parts had it (#3515).
func TestOpencodeIgnoredPartsAreNotThePersons(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"user","agent":"build"}');
insert into part values('p1','m1','{"type":"text","text":"why does the exporter drop every third retry","time":{"start":"2026-01-02T03:00:00Z"}}');
insert into message values('m2','s1',1767409260000,'{"role":"user","agent":"build"}');
insert into part values('p2','m2','{"type":"text","ignored":true,"text":"▣ DCP | -100.3K removed, +9K summary — Compression #1","time":{"start":"2026-01-02T03:01:00Z"}}');
insert into message values('m3','s1',1767409320000,'{"role":"assistant","agent":"build"}');
insert into part values('p3','m3','{"type":"text","text":"the backoff counts from zero","time":{"start":"2026-01-02T03:02:00Z"}}');`
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
	if len(users) != 1 || users[0] != "why does the exporter drop every third retry" {
		t.Errorf("user turns = %q, want the typed prompt alone", users)
	}
	for _, m := range ss[0].Messages {
		if m.Text == "▣ DCP | -100.3K removed, +9K summary — Compression #1" {
			t.Errorf("the banner is indexed under role %q", m.Role)
		}
	}
	// The turn either side of it is untouched: what goes is the banner, not the
	// conversation around it.
	if len(ss[0].Messages) != 2 {
		t.Errorf("messages = %d, want the question and the answer: %#v", len(ss[0].Messages), ss[0].Messages)
	}
}
