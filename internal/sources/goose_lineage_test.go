package sources

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The store goose 1.46 writes: summon's delegate creates a sub_agent session
// with parent_session_id, and `goose session --resume --fork` copies the
// source's messages into a new session under the same message ids and times,
// with no parent recorded.
const gooseLineageStore = `create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text, session_type text, parent_session_id text);
create table messages (id integer primary key autoincrement, message_id text, session_id text, role text, content_json text, created_timestamp integer);
insert into sessions values ('20261018_1','qparent','','/w/my-app','2026-10-18 06:36:23','2026-10-18 06:36:35','user',null);
insert into sessions values ('20261018_2','Delegated task','','/w/my-app','2026-10-18 06:36:32','2026-10-18 06:36:34','sub_agent','20261018_1');
insert into sessions values ('20261018_3','qparent','','/w/my-app','2026-10-18 06:36:49','2026-10-18 06:37:13','user',null);
insert into sessions values ('20261018_4','qparent','','/w/my-app','2026-10-18 06:38:00','2026-10-18 06:38:10','user',null);
insert into sessions values ('20261018_5','other','','/w/my-app','2026-10-18 06:39:00','2026-10-18 06:39:10','user',null);
insert into sessions values ('20261018_6','orphan','','/w/my-app','2026-10-18 06:40:00','2026-10-18 06:40:10','sub_agent',null);
insert into messages values (1,'msg_p1','20261018_1','user','[{"type":"text","text":"parent asks KOALA7"}]',1792305384);
insert into messages values (2,'chatcmpl-a','20261018_1','assistant','[{"type":"text","text":"parent answers"}]',1792305386);
insert into messages values (3,'msg_p2','20261018_1','user','[{"type":"text","text":"delegate the child file"}]',1792305392);
insert into messages values (4,'chatcmpl-b','20261018_1','assistant','[{"type":"text","text":"child done"}]',1792305395);
insert into messages values (5,'msg_c1','20261018_2','user','[{"type":"text","text":"child task PLATYPUS9"}]',1792305392);
insert into messages values (6,'chatcmpl-c','20261018_2','assistant','[{"type":"text","text":"child answer"}]',1792305394);
insert into messages values (7,'msg_p1','20261018_3','user','[{"type":"text","text":"parent asks KOALA7"}]',1792305384);
insert into messages values (8,'chatcmpl-a','20261018_3','assistant','[{"type":"text","text":"parent answers"}]',1792305386);
insert into messages values (9,'msg_p2','20261018_3','user','[{"type":"text","text":"delegate the child file"}]',1792305392);
insert into messages values (10,'chatcmpl-b','20261018_3','assistant','[{"type":"text","text":"child done"}]',1792305395);
insert into messages values (11,'msg_f1','20261018_3','user','[{"type":"text","text":"fork asks NUMBAT3"}]',1792305431);
insert into messages values (12,'chatcmpl-f','20261018_3','assistant','[{"type":"text","text":"fork answers"}]',1792305433);
insert into messages values (13,'msg_p1','20261018_4','user','[{"type":"text","text":"parent asks KOALA7"}]',1792305384);
insert into messages values (14,'chatcmpl-a','20261018_4','assistant','[{"type":"text","text":"parent answers"}]',1792305386);
insert into messages values (15,'msg_p2','20261018_4','user','[{"type":"text","text":"delegate the child file"}]',1792305392);
insert into messages values (16,'chatcmpl-b','20261018_4','assistant','[{"type":"text","text":"child done"}]',1792305395);
insert into messages values (17,'msg_f1','20261018_4','user','[{"type":"text","text":"fork asks NUMBAT3"}]',1792305431);
insert into messages values (18,'chatcmpl-f','20261018_4','assistant','[{"type":"text","text":"fork answers"}]',1792305433);
insert into messages values (19,'msg_g1','20261018_4','user','[{"type":"text","text":"fork of fork asks EMU5"}]',1792305490);
insert into messages values (20,'msg_o1','20261018_5','user','[{"type":"text","text":"unrelated session"}]',1792305540);
insert into messages values (21,'chatcmpl-a','20261018_5','assistant','[{"type":"text","text":"a provider that repeats ids"}]',1792305541);
insert into messages values (22,'msg_x1','20261018_6','user','[{"type":"text","text":"sub-agent with no parent"}]',1792305600);`

func gooseLineageSessions(t *testing.T) map[string]model.Session {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "sessions.db")
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, gooseLineageStore).CombinedOutput(); err != nil {
		t.Fatalf("create db: %v: %s", err, out)
	}
	ss, err := ParseGooseDB(db)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]model.Session{}
	for _, s := range ss {
		by[s.ID] = s
	}
	return by
}

func gooseTexts(s model.Session) string {
	b := make([]string, 0, len(s.Messages))
	for _, m := range s.Messages {
		b = append(b, m.Text)
	}
	return strings.Join(b, " | ")
}

// A goose sub-agent run was dropped whole, so blame had no record of the files
// it wrote and show printed no edge on either side (#5232).
func TestGooseSubagentRunHangsUnderItsParent(t *testing.T) {
	by := gooseLineageSessions(t)
	c, ok := by["20261018_2"]
	if !ok {
		t.Fatalf("the sub-agent run is not read; sessions: %v", by)
	}
	if c.Kind != "subagent" || c.Parent != "20261018_1" {
		t.Errorf("sub-agent kind/parent = %q/%q, want subagent/20261018_1", c.Kind, c.Parent)
	}
	if !strings.Contains(gooseTexts(c), "PLATYPUS9") {
		t.Errorf("sub-agent lost its task: %s", gooseTexts(c))
	}
	// No edge, nothing to hang it under: it stays out, as before.
	if _, ok := by["20261018_6"]; ok {
		t.Errorf("a sub_agent row with no parent leaked into recall")
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	if _, ok := gooseLineageSessions(t)["20261018_2"]; ok {
		t.Errorf("DEJA_INCLUDE_SUBAGENTS=0 still reads the sub-agent run")
	}
}

// A fork read as a full copy of its source, so blame credited the fork for
// the source's writes on an exact time tie and search found every copied turn
// twice (#5233).
func TestGooseForkKeepsOnlyItsOwnTurns(t *testing.T) {
	by := gooseLineageSessions(t)
	f := by["20261018_3"]
	if f.Kind != "fork" || f.Parent != "20261018_1" {
		t.Errorf("fork kind/parent = %q/%q, want fork/20261018_1", f.Kind, f.Parent)
	}
	if got := gooseTexts(f); strings.Contains(got, "KOALA7") || !strings.Contains(got, "NUMBAT3") {
		t.Errorf("fork turns = %s, want only its own", got)
	}
	// A fork of the fork names the fork, which holds the most of what it copied.
	g := by["20261018_4"]
	if g.Kind != "fork" || g.Parent != "20261018_3" {
		t.Errorf("fork of fork kind/parent = %q/%q, want fork/20261018_3", g.Kind, g.Parent)
	}
	if got := gooseTexts(g); strings.Contains(got, "NUMBAT3") || !strings.Contains(got, "EMU5") {
		t.Errorf("fork of fork turns = %s, want only its own", got)
	}
	// Sharing an id further in, not at the start, is not a fork.
	o := by["20261018_5"]
	if o.Kind != "" || o.Parent != "" || len(o.Messages) != 2 {
		t.Errorf("unrelated session read as %q/%q with %d messages", o.Kind, o.Parent, len(o.Messages))
	}
	if p := by["20261018_1"]; p.Kind != "" || len(p.Messages) != 4 {
		t.Errorf("source changed: %q, %d messages", p.Kind, len(p.Messages))
	}
}

// `goose session --resume --fork --edit` keeps a rewritten message's id, so a
// shared id alone is not a copy; and an incremental pass reads a fork the way
// a full one does.
func TestGooseForkEditAndSinceRead(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "sessions.db")
	edit := gooseLineageStore + `
insert into sessions values ('20261018_7','qparent','','/w/my-app','2026-10-18 06:41:00','2026-10-18 06:41:10','user',null);
insert into messages values (23,'msg_p1','20261018_7','user','[{"type":"text","text":"parent asks KOALA7"}]',1792305384);
insert into messages values (24,'chatcmpl-a','20261018_7','assistant','[{"type":"text","text":"EDITED answer WOMBAT"}]',1792305386);
insert into messages values (25,'msg_e1','20261018_7','user','[{"type":"text","text":"edited fork goes on"}]',1792305670);
insert into sessions values ('20261018_8','cron','','/w/my-app','2026-10-18 06:42:00','2026-10-18 06:42:10','scheduled',null);
insert into messages values (26,'msg_s1','20261018_8','user','[{"type":"text","text":"cron noise"}]',1792305720);`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, edit).CombinedOutput(); err != nil {
		t.Fatalf("create db: %v: %s", err, out)
	}
	full, err := ParseGooseDB(db)
	if err != nil {
		t.Fatal(err)
	}
	since, err := ParseGooseDBSince(db, time.Date(2026, 10, 18, 6, 36, 40, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	pick := func(ss []model.Session, id string) (model.Session, bool) {
		for _, s := range ss {
			if s.ID == id {
				return s, true
			}
		}
		return model.Session{}, false
	}
	for name, ss := range map[string][]model.Session{"full": full, "since": since} {
		e, _ := pick(ss, "20261018_7")
		if got := gooseTexts(e); strings.Contains(got, "KOALA7") || !strings.Contains(got, "WOMBAT") {
			t.Errorf("%s: edited fork turns = %s, want the edit kept and the copy dropped", name, got)
		}
		if e.Kind != "fork" || e.Parent != "20261018_1" {
			t.Errorf("%s: edited fork kind/parent = %q/%q", name, e.Kind, e.Parent)
		}
		f, _ := pick(ss, "20261018_3")
		if f.Kind != "fork" || strings.Contains(gooseTexts(f), "KOALA7") {
			t.Errorf("%s: fork read as %q: %s", name, f.Kind, gooseTexts(f))
		}
		if _, ok := pick(ss, "20261018_8"); ok {
			t.Errorf("%s: a scheduled run leaked into recall", name)
		}
	}
}
