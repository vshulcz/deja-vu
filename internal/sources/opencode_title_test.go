package sources

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// opencode names every session; the reader ignored the column and titled a
// session by its first user line, which for a subagent run is the whole brief
// (#3315). A thin name — "done", "ok", "Greeting", 28 of 1472 on a real store —
// is left to the index, as Continue's placeholders are (#3274).
func TestOpencodeTakesTheSessionTitleUnlessItIsThin(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text, parent_id text, directory text, title text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('ses_named',NULL,'/w','Review route overlap fix (@code-reviewer subagent)','2026-01-02T03:00:00Z','2026-01-02T03:30:00Z');
insert into message values('m1','ses_named',1767409200000,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"Read-only review of the current uncommitted RoutePilot route overlap change","time":{"start":"2026-01-02T03:00:00Z"}}');
insert into session values('ses_thin',NULL,'/w','done','2026-01-02T04:00:00Z','2026-01-02T04:30:00Z');
insert into message values('m2','ses_thin',1767412800000,'{"role":"user"}');
insert into part values('p2','m2','{"type":"text","text":"render the plots for the report","time":{"start":"2026-01-02T04:00:00Z"}}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 2 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	got := map[string]string{}
	for _, s := range ss {
		got[s.ID] = s.Title
	}
	if got["ses_named"] != "Review route overlap fix (@code-reviewer subagent)" {
		t.Errorf("named session titled %q", got["ses_named"])
	}
	if got["ses_thin"] != "" {
		t.Errorf("a thin name was taken as the title: %q", got["ses_thin"])
	}
}
