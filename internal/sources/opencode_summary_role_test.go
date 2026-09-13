package sources

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// opencode marks its own digest of a compacted conversation on the message —
// `summary: true` — and deja read it as the agent talking: 1,906 of them on one
// store, 19.6% of everything indexed in the sessions that have them. It is kept,
// because it is the only record of the turns that went away, and it is kept
// under its own role, so an ordinary question does not reach it (#3384).
func TestOpencodeCompactionSummaryIsNotSpeech(t *testing.T) {
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
insert into message values('m2','s1',1767409260000,'{"role":"assistant","agent":"build"}');
insert into part values('p2','m2','{"type":"text","text":"the backoff counts from zero","time":{"start":"2026-01-02T03:01:00Z"}}');
insert into message values('m3','s1',1767409320000,'{"role":"assistant","summary":true,"mode":"compaction","agent":"compaction"}');
insert into part values('p3','m3','{"type":"text","text":"## Goal\nBring the exporter to a state where the retry budget is three","time":{"start":"2026-01-02T03:02:00Z"}}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	var roles []string
	var summary string
	for _, m := range ss[0].Messages {
		roles = append(roles, m.Role)
		if m.Role == RoleSummary {
			summary = m.Text
		}
	}
	if summary == "" {
		t.Fatalf("the summary was dropped rather than filed under its own role: %v", roles)
	}
	for _, m := range ss[0].Messages {
		if m.Role == "assistant" && m.Text == summary {
			t.Error("the summary is still indexed as the agent's speech")
		}
	}
	// The conversation around it is untouched: what changed is the attribution
	// of one message, not what the session holds.
	if len(ss[0].Messages) != 3 {
		t.Errorf("messages = %d, want the question, the answer and the summary: %v", len(ss[0].Messages), roles)
	}
}
