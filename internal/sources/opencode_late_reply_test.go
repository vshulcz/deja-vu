package sources

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A reply whose message and part were created before the watermark and whose
// text landed after it is still asked for, and its session comes back whole —
// the index replaces a session it reads again rather than adding to it (#4207).
func TestOpencodeSinceAsksForAReplyWrittenAfterThePass(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	// The watermark the half-written pass stamped: after the part started,
	// before its text was written.
	mark := time.UnixMilli(1790858251964)
	for _, tc := range []struct {
		name, script string
	}{
		{"1.x", `create table session(id text primary key, directory text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, time_updated integer, data text);
create table part(id text primary key, message_id text, session_id text, time_created integer, time_updated integer, data text);
insert into session values('s1','/w/app',1790858248400,1790858252010);
insert into message values('m1','s1',1790858248489,1790858248489,'{"role":"user"}');
insert into part values('p1','m1','s1',1790858248489,1790858248489,'{"type":"text","text":"Reply ok. Topic: the basaltfinch rollout."}');
insert into message values('m2','s1',1790858248697,1790858252007,'{"role":"assistant"}');
insert into part values('p2','m2','s1',1790858251964,1790858251965,'{"type":"text","text":"ok","time":{"start":1790858251964,"end":1790858251965}}');
insert into session values('quiet','/w/app',1790858000000,1790858000100);
insert into message values('m3','quiet',1790858000000,1790858000000,'{"role":"user"}');
insert into part values('p3','m3','quiet',1790858000000,1790858000000,'{"type":"text","text":"an older session"}');`},
		// A store from before message and part carried time_updated: the
		// session's own stamp is what says it moved.
		{"1.x without row stamps", `create table session(id text primary key, directory text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, data text);
create table part(id text primary key, message_id text, data text);
insert into session values('s1','/w/app',1790858248400,1790858252010);
insert into message values('m1','s1',1790858248489,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"Reply ok. Topic: the basaltfinch rollout."}');
insert into message values('m2','s1',1790858248697,'{"role":"assistant"}');
insert into part values('p2','m2','{"type":"text","text":"ok","time":{"start":1790858251964,"end":1790858251965}}');
insert into session values('quiet','/w/app',1790858000000,1790858000100);
insert into message values('m3','quiet',1790858000000,'{"role":"user"}');
insert into part values('p3','m3','{"type":"text","text":"an older session"}');`},
		{"2.x", `create table session_v2(id text primary key, directory text, time_created integer, time_updated integer);
create table session_message(id text primary key, session_id text, type text, seq integer, time_created integer, time_updated integer, data text);
insert into session_v2 values('s1','/w/app',1790858248400,1790858252010);
insert into session_message values('m1','s1','user',1,1790858248489,1790858248489,'{"text":"Reply ok. Topic: the basaltfinch rollout.","time":{"created":1790858248489}}');
insert into session_message values('m2','s1','assistant',2,1790858248697,1790858252007,'{"content":[{"type":"text","text":"ok","time":{"created":1790858251964}}],"time":{"created":1790858248697}}');
insert into session_v2 values('quiet','/w/app',1790858000000,1790858000100);
insert into session_message values('m3','quiet','user',1,1790858000000,1790858000000,'{"text":"an older session","time":{"created":1790858000000}}');`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "opencode.db")
			if out, err := exec.Command("sqlite3", db, tc.script).CombinedOutput(); err != nil {
				t.Fatalf("sqlite: %v %s", err, out)
			}
			for _, read := range []struct {
				name  string
				parse func(string, time.Time) ([]model.Session, error)
			}{{"opencode", ParseOpencodeDBSince}, {"kilocode", ParseKiloDBSince}, {"zcode", ParseZCodeDBSince}} {
				ss, err := read.parse(db, mark)
				if err != nil {
					t.Fatalf("%s: %v", read.name, err)
				}
				if len(ss) != 1 || ss[0].ID != "s1" {
					t.Fatalf("%s: want s1 alone, got %d sessions", read.name, len(ss))
				}
				var texts []string
				for _, m := range ss[0].Messages {
					texts = append(texts, m.Role+": "+m.Text)
				}
				if got := strings.Join(texts, " | "); got != "user: Reply ok. Topic: the basaltfinch rollout. | assistant: ok" {
					t.Errorf("%s: the session did not come back whole: %q", read.name, got)
				}
			}
		})
	}
}

// The watermark is a session's own stamp and the comparison is strict, so a row
// stamped in the same millisecond as it was never asked for. The read starts a
// few seconds back; a session read twice replaces itself (#4207).
func TestOpencodeSinceReadsARowStampedAtTheWatermark(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text primary key, directory text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, time_updated integer, data text);
create table part(id text primary key, message_id text, session_id text, time_created integer, time_updated integer, data text);
insert into session values('s1','/w/app',1790858248400,1790858252010);
insert into message values('m1','s1',1790858252010,1790858252010,'{"role":"user"}');
insert into part values('p1','m1','s1',1790858252010,1790858252010,'{"type":"text","text":"same millisecond"}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite: %v %s", err, out)
	}
	ss, err := ParseOpencodeDBSince(db, time.UnixMilli(1790858252010))
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Errorf("a row stamped at the watermark came back in %d sessions", len(ss))
	}
}
