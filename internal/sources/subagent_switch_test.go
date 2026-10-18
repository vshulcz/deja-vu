package sources

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// switchCase is one reader's sub-agent run: setup writes the store and returns
// the path the registry parses, child is the run's session id, and long says
// the run has enough turns that the default cut shows.
type switchCase struct {
	kind  string
	child string
	long  bool
	// fork marks a control: a session the person went on in, which the
	// switch leaves whole.
	fork  bool
	setup func(t *testing.T) string
}

const switchTurns = 10

func switchJSONL(n int, line func(i int) string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(line(i))
		b.WriteString("\n")
	}
	return b.String()
}

func writeSwitchFile(t *testing.T, p, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func switchSQLite(t *testing.T, name, script string) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), name)
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	return db
}

func switchCases() []switchCase {
	return []switchCase{
		{kind: "gemini", child: "24b39ab6-b48a-48e4-80f9-0ba7bb24acc6", long: true, setup: func(t *testing.T) string {
			_, chats := geminiTree(t)
			head := `{"sessionId":"24b39ab6-b48a-48e4-80f9-0ba7bb24acc6","projectHash":"abc","startTime":"2026-10-17T21:48:40.460Z","lastUpdated":"2026-10-17T21:48:40.460Z","kind":"subagent","directories":["/w/p"]}` + "\n"
			body := switchJSONL(switchTurns, func(i int) string {
				typ := "user"
				content := fmt.Sprintf(`[{"text":"zebrafish step %d"}]`, i)
				if i%2 == 1 {
					typ, content = "gemini", fmt.Sprintf(`"zebrafish answer %d"`, i)
				}
				return fmt.Sprintf(`{"id":"m%d","timestamp":"2026-10-17T21:48:%02d.000Z","type":"%s","content":%s,"model":"gemini-3"}`, i, 41+i, typ, content)
			})
			return writeSwitchFile(t, filepath.Join(chats, "f7b7a064-d884-4582-a228-e95a8c1ac4f3", "24b39ab6-b48a-48e4-80f9-0ba7bb24acc6.jsonl"), head+body)
		}},
		{kind: "codex", child: "child", long: true, setup: func(t *testing.T) string {
			head := `{"timestamp":"2026-10-02T07:01:00Z","type":"session_meta","payload":{"id":"child","cwd":"/w","source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent","depth":1}}}}}` + "\n"
			body := switchJSONL(switchTurns, func(i int) string {
				role, typ := "user", "input_text"
				if i%2 == 1 {
					role, typ = "assistant", "output_text"
				}
				return fmt.Sprintf(`{"timestamp":"2026-10-02T07:01:%02dZ","type":"response_item","payload":{"type":"message","role":"%s","content":[{"type":"%s","text":"zebrafish turn %d"}]}}`, i+1, role, typ, i)
			})
			return writeSwitchFile(t, filepath.Join(t.TempDir(), "rollout-2026-10-02T07-01-00-child.jsonl"), head+body)
		}},
		{kind: "openclaw", child: "kid", long: true, setup: func(t *testing.T) string {
			dir := filepath.Join(t.TempDir(), "main", "sessions")
			writeSwitchFile(t, filepath.Join(dir, "sessions.json"), `{"agent:main:main":{"sessionId":"p"},"agent:main:subagent:1":{"sessionId":"kid","spawnedBy":"agent:main:main"}}`)
			head := `{"type":"session","version":3,"id":"kid","timestamp":"2026-07-20T09:00:00Z","cwd":"/work/app"}` + "\n"
			body := switchJSONL(switchTurns, func(i int) string {
				role := "user"
				if i%2 == 1 {
					role = "assistant"
				}
				return fmt.Sprintf(`{"type":"message","id":"m%d","timestamp":"2026-07-20T09:00:%02dZ","message":{"role":"%s","content":"zebrafish turn %d"}}`, i, i+1, role, i)
			})
			return writeSwitchFile(t, filepath.Join(dir, "kid.jsonl"), head+body)
		}},
		{kind: "pi", child: "kid", long: true, setup: func(t *testing.T) string {
			head := `{"type":"session","version":3,"id":"kid","timestamp":"2026-07-20T09:00:00Z","cwd":"/work/app","rlmDepth":1,"parentSession":"/work/p.jsonl"}` + "\n"
			body := switchJSONL(switchTurns, func(i int) string {
				role := "user"
				if i%2 == 1 {
					role = "assistant"
				}
				return fmt.Sprintf(`{"type":"message","id":"m%d","timestamp":"2026-07-20T09:00:%02dZ","message":{"role":"%s","content":"zebrafish turn %d"}}`, i, i+1, role, i)
			})
			return writeSwitchFile(t, filepath.Join(t.TempDir(), "kid.jsonl"), head+body)
		}},
		{kind: "kiro-cli", child: "e012b5f2-0f80-45a3-a1f6-eccc522bb35e", long: true, setup: func(t *testing.T) string {
			dir := t.TempDir()
			id := "e012b5f2-0f80-45a3-a1f6-eccc522bb35e"
			writeSwitchFile(t, filepath.Join(dir, id+".json"), `{"session_id":"`+id+`","cwd":"/w/app","parent_session_id":"136cf72a-585d-4047-997f-d37a0d0f194a","session_created_reason":"subagent"}`)
			body := switchJSONL(switchTurns, func(i int) string {
				return fmt.Sprintf(`{"version":"v1","kind":"Prompt","data":{"message_id":"m%d","content":[{"kind":"text","data":"zebrafish step %d"}],"meta":{"timestamp":%d}}}`, i, i, 1792281752+i)
			})
			return writeSwitchFile(t, filepath.Join(dir, id+".jsonl"), body)
		}},
		{kind: "codewhale", child: "child", long: true, fork: true, setup: func(t *testing.T) string {
			root := t.TempDir()
			t.Setenv("DEJA_CODEWHALE_ROOT", root)
			var msgs []string
			for i := 0; i < switchTurns; i++ {
				role := "user"
				if i%2 == 1 {
					role = "assistant"
				}
				msgs = append(msgs, fmt.Sprintf(`{"role":"%s","content":[{"type":"text","text":"zebrafish turn %d"}]}`, role, i))
			}
			return writeSwitchFile(t, filepath.Join(root, "child.json"), `{"metadata":{"id":"child","title":"fork","created_at":"2026-09-19T11:00:00Z","updated_at":"2026-09-19T11:01:00Z","workspace":"/w/fork","parent_session_id":"parent-1"},"messages":[`+strings.Join(msgs, ",")+`]}`)
		}},
		{kind: "hermes", child: "s-sub", long: true, setup: func(t *testing.T) string {
			var rows []string
			for i := 0; i < switchTurns; i++ {
				role := "user"
				if i%2 == 1 {
					role = "assistant"
				}
				rows = append(rows, fmt.Sprintf("('s-sub','%s','zebrafish turn %d',%d)", role, i, 1785000010+i))
			}
			return writeHermesDB(t, filepath.Join(t.TempDir(), "p"), `
				CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT, parent_session_id TEXT, cwd TEXT);
				INSERT INTO sessions VALUES ('s-cli','cli',NULL,'/w/app'),('s-sub','subagent','s-cli','/w/app');
				INSERT INTO messages (session_id,role,content,timestamp) VALUES
				 ('s-cli','user','plan the migration',1785000000),`+strings.Join(rows, ",")+`;`)
		}},
		{kind: "deepseek", child: "286f51ca-3e5c-437d-8e6b-c9d5cfb5e4d3", setup: func(t *testing.T) string {
			child := strings.Replace(deepSeekLog,
				`"id":"session-eaf5c9ac-0e47-4d2f-b982-8bae306062d1","createdAt":1787320263519,"cwd":"/work/pgbouncer-lab","delegationDepth":0}`,
				`"id":"286f51ca-3e5c-437d-8e6b-c9d5cfb5e4d3","createdAt":1787320263519,"cwd":"/work/pgbouncer-lab","parentSession":"session-c559c777-a703-4cfa-9f2e-f4b9ebf675cc","origin":"subagent","delegationDepth":1}`, 1)
			return writeDeepSeekSession(t, t.TempDir(), "child", child, false)
		}},
		{kind: "grok", child: "c1", setup: func(t *testing.T) string {
			root := t.TempDir()
			t.Setenv("DEJA_GROK_ROOT", root)
			at := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
			turn := `{"timestamp":1,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"zebrafish"}}}}` + "\n"
			return grokLineageSession(t, root, "c1", `{"info":{"id":"c1","cwd":"/work/ledger"},"session_kind":"subagent","created_at":"`+at+`"}`, turn)
		}},
		{kind: "crush", child: "child", setup: func(t *testing.T) string {
			sql := "insert into sessions values ('parent',null,'top',1,0,0,0.0,1784282400,1784282400,null,null);\n"
			sql += "insert into sessions values ('child','parent','sub',1,0,0,0.0,1784282401,1784282401,null,null);\n"
			sql += crushInsert(t, "m1", "parent", "user", 1784282400, []any{map[string]any{"type": "text", "data": map[string]any{"text": "delegate it"}}})
			sql += crushInsert(t, "m2", "child", "assistant", 1784282401, []any{map[string]any{"type": "text", "data": map[string]any{"text": "zebrafish"}}})
			return crushStore(t, "demo", sql)
		}},
		{kind: "opencode", child: "ses_child", setup: func(t *testing.T) string {
			return switchSQLite(t, "opencode.db", `create table session(id text, parent_id text, directory text, title text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('ses_parent',NULL,'/w','top','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into session values('ses_child','ses_parent','/w','sub (@general subagent)','2026-01-02T03:10:00Z','2026-01-02T03:20:00Z');
insert into message values('m1','ses_parent',1767409200000,'{"role":"user","agent":"build"}');
insert into part values('p1','m1','{"type":"text","text":"review the screenshots","time":{"start":"2026-01-02T03:00:00Z"}}');
insert into message values('m2','ses_child',1767409800000,'{"role":"user","agent":"general"}');
insert into part values('p2','m2','{"type":"text","text":"zebrafish defects","time":{"start":"2026-01-02T03:10:00Z"}}');`)
		}},
		{kind: "goose-db", child: "20261018_2", setup: func(t *testing.T) string {
			return switchSQLite(t, "sessions.db", gooseLineageStore)
		}},
		{kind: "devin", child: "registry-devin-sub:ag-worker", setup: func(t *testing.T) string {
			devinHome(t)
			db, _ := devinTestDB(t, devinFixtureSQL(t))
			return db
		}},
	}
}

// parseThroughRegistry parses p the way an index pass does: through the
// registry's kind, which is where the switch is applied.
func parseThroughRegistry(t *testing.T, kind, p string) []model.Session {
	t.Helper()
	var found *FileKind
	for _, h := range Registry() {
		for i, k := range h.Kinds {
			if k.Name != kind {
				continue
			}
			if found == nil || k.Match(p) {
				found = &h.Kinds[i]
			}
		}
	}
	if found == nil {
		t.Fatalf("no kind %s", kind)
	}
	ss, err := found.Parse(p, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", p, err)
	}
	return ss
}

// DEJA_INCLUDE_SUBAGENTS=0 left out the sub-agents of eight readers and kept
// the other twelve, and the default read some of them whole (#5301). Every
// reader that marks a spawned run now gets the same switch: =0 leaves it out,
// unset keeps its task, edits and last turns, =1 keeps all of it.
func TestSubagentSwitchMeansTheSameInEveryReader(t *testing.T) {
	for _, c := range switchCases() {
		t.Run(c.kind, func(t *testing.T) {
			t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
			p := c.setup(t)
			count := func(mode string) int {
				t.Setenv("DEJA_INCLUDE_SUBAGENTS", mode)
				for _, s := range parseThroughRegistry(t, c.kind, p) {
					if s.ID == c.child {
						if SpawnedKind(s.Kind) == c.fork {
							t.Fatalf("%s read with kind %q", s.ID, s.Kind)
						}
						return len(s.Messages)
					}
				}
				return -1
			}
			whole, cut, off := count("1"), count(""), count("0")
			if c.fork {
				if whole != switchTurns || cut != whole || off != whole {
					t.Errorf("a fork is not a sub-agent: =1 %d, default %d, =0 %d messages, want %d each", whole, cut, off, switchTurns)
				}
				return
			}
			if whole <= 0 {
				t.Fatalf("=1: the sub-agent run is not read")
			}
			if cut <= 0 {
				t.Fatalf("default: the sub-agent run is not read")
			}
			if off != -1 {
				t.Errorf("=0: the sub-agent run is still read, %d messages", off)
			}
			if c.long && (cut >= whole || cut > SubagentTailKept+1) {
				t.Errorf("default kept %d of %d messages, want the task and the last %d turns", cut, whole, SubagentTailKept)
			}
		})
	}
}

func TestApplySubagentSwitchLeavesOtherSessionsAlone(t *testing.T) {
	msgs := make([]model.Message, 9)
	for i := range msgs {
		msgs[i] = model.Message{Role: "user", Text: fmt.Sprint(i)}
	}
	in := []model.Session{
		{ID: "top", Messages: msgs},
		{ID: "fork", Kind: "fork", Messages: msgs},
		{ID: "child", Kind: "subagent", Messages: msgs},
	}
	byID := func(ss []model.Session) map[string]int {
		out := map[string]int{}
		for _, s := range ss {
			out[s.ID] = len(s.Messages)
		}
		return out
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	if got := byID(ApplySubagentSwitch(in)); len(got) != 2 || got["top"] != 9 || got["fork"] != 9 {
		t.Errorf("=0: %v, want top and fork whole and no child", got)
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	if got := byID(ApplySubagentSwitch(in)); got["top"] != 9 || got["fork"] != 9 || got["child"] != SubagentTailKept+1 {
		t.Errorf("default: %v", got)
	}
	if len(in[2].Messages) != 9 {
		t.Errorf("the switch changed the caller's slice: %d", len(in[2].Messages))
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	if got := byID(ApplySubagentSwitch(in)); got["child"] != 9 {
		t.Errorf("=1: %v", got)
	}
}
