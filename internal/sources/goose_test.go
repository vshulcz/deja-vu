package sources

import (
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// goose stores both sides of every file change: the developer extension's
// `edit` takes path, before and after, and `write` takes path and content
// (goose 1.46.0, read back from a store goose had just written). Only the path
// was kept, so restore and line-level blame had nothing from goose (#4265).
// The text_editor tool goose shipped before it spells the same things
// old_str, new_str and file_text.
func TestGooseEditAndWriteCallsLeaveEditAndWroteRecords(t *testing.T) {
	for _, tc := range []struct {
		name, raw  string
		edit       string
		wroteLines []string
	}{
		{
			name: "edit",
			raw: `[{"type":"toolRequest","id":"call_1","toolCall":{"status":"success","value":{"name":"edit","arguments":` +
				`{"path":"/tmp/proj/main.go","before":"func retry() {}","after":"func retry() {\n\tfor i := 0; i < 3; i++ {\n\t}\n}"}}},` +
				`"_meta":{"goose_extension":"developer"}}]`,
			edit:       "/tmp/proj/main.go\nfunc retry() {}",
			wroteLines: []string{"\tfor i := 0; i < 3; i++ {"},
		},
		{
			name: "write",
			raw: `[{"type":"toolRequest","id":"call_2","toolCall":{"status":"success","value":{"name":"write","arguments":` +
				`{"path":"/tmp/proj/NOTES.md","content":"retry loop: cap at 3 attempts\n"}}},"_meta":{"goose_extension":"developer"}}]`,
			wroteLines: []string{"retry loop: cap at 3 attempts"},
		},
		{
			name: "text_editor str_replace",
			raw: `[{"type":"toolRequest","id":"t2","toolCall":{"status":"success","value":{"name":"developer__text_editor","arguments":` +
				`{"command":"str_replace","path":"/w/app/queue.go","old_str":"size := 10","new_str":"pool := make(chan conn, maxPoolSize)"}}}}]`,
			edit:       "/w/app/queue.go\nsize := 10",
			wroteLines: []string{"pool := make(chan conn, maxPoolSize)"},
		},
		{
			// goose 1.10–1.25 takes a unified diff on str_replace, and its
			// schema calls that the preferred way to edit (#4287).
			name: "text_editor str_replace diff",
			raw: `[{"type":"toolRequest","id":"t5","toolCall":{"status":"success","value":{"name":"developer__text_editor","arguments":` +
				`{"command":"str_replace","path":"/tmp/proj/retry.go","diff":"--- a/retry.go\n+++ b/retry.go\n@@ -1 +1,3 @@\n-func retry() {}\n+func retry() {\n+\tfor i := 0; i < 3; i++ {}\n+}\n"}}}}]`,
			edit:       "/tmp/proj/retry.go\nfunc retry() {}",
			wroteLines: []string{"\tfor i := 0; i < 3; i++ {}"},
		},
		{
			// The diff wins over old_str and new_str when a call has both,
			// as it does in goose's text_editor.
			name: "text_editor str_replace diff over old_str",
			raw: `[{"type":"toolRequest","id":"t6","toolCall":{"status":"success","value":{"name":"developer__text_editor","arguments":` +
				`{"command":"str_replace","path":"/tmp/proj/retry.go","old_str":"zzz","new_str":"yyy","diff":"--- a/retry.go\n+++ b/retry.go\n@@ -1 +1 @@\n-func retry() {}\n+func retry() { backoff() }\n"}}}}]`,
			edit:       "/tmp/proj/retry.go\nfunc retry() {}",
			wroteLines: []string{"func retry() { backoff() }"},
		},
		{
			name: "text_editor write",
			raw: `[{"type":"toolRequest","id":"t3","toolCall":{"status":"success","value":{"name":"developer__text_editor","arguments":` +
				`{"command":"write","path":"/w/app/pool.go","file_text":"package app\n\nconst maxPoolSize = 40 // raised after the load test\n"}}}}]`,
			wroteLines: []string{"const maxPoolSize = 40 // raised after the load test"},
		},
	} {
		s := gooseSessionFrom(t, "assistant", tc.raw)
		var edits, wrote []string
		for _, m := range s.Messages {
			switch m.Role {
			case RoleEdit:
				edits = append(edits, m.Text)
			case RoleWrote:
				wrote = append(wrote, m.Text)
			}
		}
		if tc.edit == "" && len(edits) != 0 {
			t.Errorf("%s: edit records %q, want none", tc.name, edits)
		}
		if tc.edit != "" && (len(edits) != 1 || edits[0] != tc.edit) {
			t.Errorf("%s: edit records %q, want %q", tc.name, edits, tc.edit)
		}
		if len(wrote) != 1 {
			t.Fatalf("%s: wrote records %q, want one", tc.name, wrote)
		}
		for _, line := range tc.wroteLines {
			h, _ := HashWrittenLine(line)
			if _, has := WroteRecordHas(wrote[0], h); !has {
				t.Errorf("%s: wrote record %q lacks %q", tc.name, wrote[0], line)
			}
		}
		if !strings.HasPrefix(wrote[0], "/") {
			t.Errorf("%s: wrote record %q does not start with the path", tc.name, wrote[0])
		}
	}
}

// text_editor carries every argument any of its commands takes, and goose
// reads only the ones the command names: a view or an undo with stray old_str
// or new_str changed nothing, so it leaves no edit and no wrote record.
func TestGooseTextEditorReadsOnlyWhatTheCommandTakes(t *testing.T) {
	for _, command := range []string{"view", "undo_edit"} {
		raw := `[{"type":"toolRequest","id":"t4","toolCall":{"status":"success","value":{"name":"developer__text_editor","arguments":` +
			`{"command":"` + command + `","path":"/w/app/queue.go","old_str":"size := 10","new_str":"pool := make(chan conn, maxPoolSize)","file_text":"package app\n\nconst maxPoolSize = 40\n","edits":[{"old_str":"zzz","new_str":"pool := make(chan conn, maxPoolSize)"}]}}}}]`
		s := gooseSessionFrom(t, "assistant", raw)
		for _, m := range s.Messages {
			if m.Role == RoleEdit || m.Role == RoleWrote {
				t.Errorf("%s: %s record %q, want none", command, m.Role, m.Text)
			}
		}
	}
}

// A diff naming several files is applied under the call's path, which is then
// a directory: each file gets its own edit and wrote record (#4287).
func TestGooseTextEditorMultiFileDiff(t *testing.T) {
	raw := `[{"type":"toolRequest","id":"t7","toolCall":{"status":"success","value":{"name":"developer__text_editor","arguments":` +
		`{"command":"str_replace","path":"/tmp/proj","diff":"--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old a\n+retryBudget := 3 * time.Second\n--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n-old b\n+poolSize := runtime.NumCPU() * 4\n"}}}}]`
	s := gooseSessionFrom(t, "assistant", raw)
	var edits, wrote []string
	for _, m := range s.Messages {
		switch m.Role {
		case RoleEdit:
			edits = append(edits, m.Text)
		case RoleWrote:
			wrote = append(wrote, m.Text)
		}
	}
	if strings.Join(edits, "|") != "/tmp/proj/a.go\nold a|/tmp/proj/b.go\nold b" {
		t.Errorf("edit records %q", edits)
	}
	if len(wrote) != 2 || !strings.HasPrefix(wrote[0], "/tmp/proj/a.go\n") || !strings.HasPrefix(wrote[1], "/tmp/proj/b.go\n") {
		t.Errorf("wrote records %q", wrote)
	}
}

// The diff's headers go under the base goose uses, in the path's own
// convention, without repeating the directories the two share.
func TestGooseTextEditorDiffPaths(t *testing.T) {
	for _, tc := range []struct{ call, header, want string }{
		{"/w/proj/retry.go", "b/retry.go", "/w/proj/retry.go"},
		{"/w/proj/src/retry.go", "b/src/retry.go", "/w/proj/src/retry.go"},
		{"/w/proj/src", "b/src/a.go", "/w/proj/src/a.go"},
		{"/w/proj", "b/pkg/a.go", "/w/proj/pkg/a.go"},
		{`C:\w\proj\src\retry.go`, "b/src/retry.go", `C:\w\proj\src\retry.go`},
		{"/w/proj/retry.go", "/abs/other.go", "/abs/other.go"},
		// A relative path is under goose's working directory, and a file's
		// base is that directory, not the file.
		{"main.go", "b/util.go", "util.go"},
	} {
		diff := "--- " + tc.header + "\n+++ " + tc.header + "\n@@ -1 +1 @@\n-x := 1\n+x := 2\n"
		if files, _, _ := gooseTextEditorDiff(tc.call, diff); len(files) != 1 || files[0] != tc.want {
			t.Errorf("%s with %s: files %q, want %q", tc.call, tc.header, files, tc.want)
		}
	}
}

// goose hands its hooks no tool output, so the failure a Stop hook answers is
// read from the store: the newest failed shell response of the turn, never one
// from before the person's last message.
func TestGooseTurnFailureReadsTheTurnsFailedCommand(t *testing.T) {
	if !SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("DEJA_GOOSE_ROOT", filepath.Join(root, "goose"))
	dir := filepath.Join(root, "goose", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "sessions.db")
	fail := func(text string) string {
		return `[{"type":"toolResponse","id":"c","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"` + text + `"}],"structuredContent":{"exit_code":2},"isError":true}}}]`
	}
	sql := `create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert into sessions values ('s1','n','d','/work/app','2026-10-07T10:00:00Z','2026-10-07T10:05:00Z');
insert into sessions values ('s2','n','d','/work/app','2026-10-07T10:00:00Z','2026-10-07T10:05:00Z');
insert into messages values (1,'s1','user','[{"type":"text","text":"build it"}]',100);
insert into messages values (2,'s1','user','` + fail("old failure from turn one") + `',101);
insert into messages values (3,'s1','user','[{"type":"text","text":"<turn-context>now</turn-context>and again"}]',102);
insert into messages values (4,'s1','assistant','[{"type":"toolRequest","id":"c","toolCall":{"status":"success","value":{"name":"developer__shell","arguments":{"command":"make"}}}}]',103);
insert into messages values (5,'s1','user','` + fail("panic: sql: database is closed") + `',104);
insert into messages values (6,'s2','user','[{"type":"text","text":"build it"}]',100);
insert into messages values (7,'s2','user','` + fail("old failure from turn one") + `',101);
insert into messages values (8,'s2','user','[{"type":"text","text":"something else"}]',102);`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("create db: %v: %s", err, out)
	}
	if got := GooseTurnFailure("s1"); got != "panic: sql: database is closed" {
		t.Errorf("s1 turn failure = %q", got)
	}
	if got := GooseTurnFailure("s2"); got != "" {
		t.Errorf("a failure from before the person's last message came back: %q", got)
	}
	if got := GooseTurnFailure("missing"); got != "" {
		t.Errorf("an unknown session answered %q", got)
	}
}

// The legacy `.jsonl` store holds the same content parts the database does —
// goose changed where a session lives, not what a message is made of. Both
// readers go through gooseParts, and this is the half a real store on this
// machine could not cover: goose has written to sqlite since long before 1.48.
const gooseJSONLWithTools = `{"description":"build","id":"20250724_9","created_at":"2026-07-24T10:00:00Z","updated_at":"2026-07-24T10:00:09Z","working_dir":"/workspace/api","message_count":4}
{"id":"m1","role":"user","created":1784278801,"content":[{"type":"text","text":"the build keeps failing, run it"}]}
{"id":"m2","role":"assistant","created":1784278802,"content":[{"type":"text","text":"Running the build."},{"type":"toolRequest","id":"t1","toolCall":{"status":"success","value":{"name":"Bash","arguments":{"command":"go build ./... && echo built"}}}}]}
{"id":"m3","role":"user","created":1784278803,"content":[{"type":"toolResponse","id":"t1","toolResult":{"status":"success","value":{"resultType":"complete","content":[{"type":"text","text":"undefined: snorblefunc in vendor/blarg/api.go"}],"isError":false}}}]}
{"id":"m4","role":"user","created":1784278804,"content":[{"type":"text","text":"<turn-context>\n<working-directory>/workspace/api</working-directory>\n</turn-context>"}]}
`

func TestTheLegacyGooseFileKeepsCommandsAndOutput(t *testing.T) {
	_, jsonl := gooseFixture(t)
	if err := os.WriteFile(jsonl, []byte(gooseJSONLWithTools), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGooseFile(jsonl)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	roles := make([]string, 0, len(ss[0].Messages))
	byRole := map[string]string{}
	for _, m := range ss[0].Messages {
		roles = append(roles, m.Role)
		byRole[m.Role] = m.Text
	}
	if byRole[RoleCommand] != "$ go build ./... && echo built" {
		t.Errorf("command = %q (roles %v)", byRole[RoleCommand], roles)
	}
	if byRole[RoleToolOutput] != "undefined: snorblefunc in vendor/blarg/api.go" {
		t.Errorf("tool output = %q (roles %v)", byRole[RoleToolOutput], roles)
	}
	// The turn envelope was the whole of the last message, so nothing of it
	// should have been filed as something the person said.
	for _, m := range ss[0].Messages {
		if m.Role == "user" && len(m.Text) > 0 && m.Text[0] == '<' {
			t.Errorf("the turn envelope was indexed as speech: %q", m.Text)
		}
	}
}

// Resuming past the header must not change what the tail is made of: the
// offset path reads the same parts as a whole read.
func TestAResumedLegacyGooseReadKeepsTheSameParts(t *testing.T) {
	_, jsonl := gooseFixture(t)
	if err := os.WriteFile(jsonl, []byte(gooseJSONLWithTools), 0o644); err != nil {
		t.Fatal(err)
	}
	whole, err := ParseGooseFile(jsonl)
	if err != nil || len(whole) != 1 {
		t.Fatalf("whole read: %v %#v", err, whole)
	}
	// Past the header and the first two messages.
	lines := 0
	offset := 0
	for i, c := range gooseJSONLWithTools {
		if c == '\n' {
			lines++
			if lines == 3 {
				offset = i + 1
				break
			}
		}
	}
	tail, err := ParseGooseFileFromOffset(jsonl, int64(offset))
	if err != nil || len(tail) != 1 {
		t.Fatalf("resumed read: %v %#v", err, tail)
	}
	if tail[0].ID != whole[0].ID || tail[0].Project != whole[0].Project {
		t.Errorf("resumed identity %q/%q, whole %q/%q",
			tail[0].ID, tail[0].Project, whole[0].ID, whole[0].Project)
	}
	found := false
	for _, m := range tail[0].Messages {
		if m.Role == RoleToolOutput {
			found = true
		}
	}
	if !found {
		t.Errorf("the resumed tail lost the tool output: %#v", tail[0].Messages)
	}
}

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

// The shapes below are goose 1.48.0's own, read off a real store: a transcript
// was imported with `goose session import` and the row read back, rather than
// guessed from its source.
const (
	gooseToolRequestJSON = `[{"type":"text","text":"Running the build now."},` +
		`{"type":"toolRequest","id":"t1","toolCall":{"status":"success","value":` +
		`{"name":"Bash","arguments":{"command":"go build ./... && echo built"}}}}]`
	gooseToolResponseJSON = `[{"type":"toolResponse","id":"t1","toolResult":{"status":"success","value":` +
		`{"resultType":"complete","content":[{"type":"text","text":"undefined: snorblefunc in vendor/blarg/api.go\nexit status 2"}],` +
		`"isError":false}}}]`
)

// goose stores the command it ran and what the command printed in the same
// column as speech, and only `text` parts were read — so a goose store
// contributed no commands and no tool output at all. `how` had nothing to
// answer from, and friction and the fix pairs never saw the error a build
// printed.
func TestGooseKeepsTheCommandAndWhatItPrinted(t *testing.T) {
	p := gooseParts(gooseToolRequestJSON)
	speech, toolOut, commands := p.speech, p.toolOut, p.commands
	if speech != "Running the build now." {
		t.Errorf("speech = %q", speech)
	}
	if len(commands) != 1 || commands[0] != "$ go build ./... && echo built" {
		t.Errorf("commands = %q", commands)
	}
	if toolOut != "" {
		t.Errorf("a request is not output: %q", toolOut)
	}

	p = gooseParts(gooseToolResponseJSON)
	speech, toolOut, commands = p.speech, p.toolOut, p.commands
	if !strings.Contains(toolOut, "undefined: snorblefunc") || !strings.Contains(toolOut, "exit status 2") {
		t.Errorf("tool output = %q", toolOut)
	}
	if speech != "" || len(commands) != 0 {
		t.Errorf("a response is not speech or a command: %q %q", speech, commands)
	}
}

// goose files a tool result under the user's own role, the way Claude Code
// does. Indexed as speech it reads as something the person typed, and friction
// counts it as a person's words rather than a printout.
func TestAGooseToolResultIsNotFiledAsSpeech(t *testing.T) {
	s := gooseSessionFrom(t, "user", gooseToolResponseJSON)
	if len(s.Messages) != 1 {
		t.Fatalf("want one message, got %d: %#v", len(s.Messages), s.Messages)
	}
	if s.Messages[0].Role != RoleToolOutput {
		t.Errorf("role = %q, want %q", s.Messages[0].Role, RoleToolOutput)
	}
}

// The command is its own record, so `how` and the fix pairs can find it, and
// it comes after the speech of the turn that ran it.
func TestAGooseCommandIsItsOwnRecord(t *testing.T) {
	s := gooseSessionFrom(t, "assistant", gooseToolRequestJSON)
	if len(s.Messages) != 2 {
		t.Fatalf("want speech and a command, got %#v", s.Messages)
	}
	if s.Messages[0].Role != "assistant" || s.Messages[1].Role != RoleCommand {
		t.Errorf("roles = %q, %q", s.Messages[0].Role, s.Messages[1].Role)
	}
}

// An editor call names the file it is about to change, which is what blame
// reads. Both spellings, because an extension's tool may take either.
func TestAGooseEditorCallNamesItsFile(t *testing.T) {
	for _, key := range []string{"path", "file_path"} {
		raw := `[{"type":"toolRequest","id":"t2","toolCall":{"status":"success","value":` +
			`{"name":"developer__text_editor","arguments":{"` + key + `":"/w/app/queue.go","command":"str_replace"}}}}]`
		paths := gooseParts(raw).paths
		if len(paths) != 1 || paths[0] != "/w/app/queue.go" {
			t.Errorf("%s: paths = %q", key, paths)
		}
	}
}

// The error side of a result is the half friction and the fix pairs are built
// on, so it cannot be dropped for not being a success.
func TestAFailedGooseToolStillCarriesWhatWentWrong(t *testing.T) {
	raw := `[{"type":"toolResponse","id":"t3","toolResult":{"status":"error",` +
		`"error":{"code":-32603,"message":"command not found: shellcheck"}}}]`
	toolOut := gooseParts(raw).toolOut
	if !strings.Contains(toolOut, "command not found: shellcheck") {
		t.Errorf("tool output = %q", toolOut)
	}
}

// goose writes the turn envelope — clock, working directory, standing tasks —
// as a message of the user's own role, so every turn on a real store added one
// and deja indexed it as something the person said.
func TestTheGooseTurnEnvelopeIsNotIndexedAsSpeech(t *testing.T) {
	raw := `[{"type":"text","text":"<turn-context>\n<current-time>2026-09-02 01:07:00 +03:00</current-time>\n<working-directory>/private/tmp</working-directory>\n</turn-context>"}]`
	speech := gooseParts(raw).speech
	if speech != "" {
		t.Errorf("the envelope was kept as speech: %q", speech)
	}

	// Stripped, not dropped whole: a turn that carries the envelope and real
	// words has to keep the words.
	both := `[{"type":"text","text":"<turn-context>\n<current-time>x</current-time>\n</turn-context>\nraise the pool to 40"}]`
	speech = gooseParts(both).speech
	if speech != "raise the pool to 40" {
		t.Errorf("speech = %q, want the words without the envelope", speech)
	}
}

// A row deja can make nothing of stays out rather than arriving empty.
func TestAGooseRowWithNothingToIndexIsSkipped(t *testing.T) {
	for _, raw := range []string{
		`[{"type":"thinking","thinking":"..."}]`,
		`[]`,
		`[{"type":"text","text":"   "}]`,
	} {
		if p := gooseParts(raw); !p.empty() {
			t.Errorf("%s produced %+v", raw, p)
		}
	}
}

// gooseSessionFrom runs one row through the same appender both readers use.
func gooseSessionFrom(t *testing.T, role, raw string) model.Session {
	t.Helper()
	var s model.Session
	appendGooseParts(&s, role, time.Unix(0, 0), gooseParts(raw), nil)
	return s
}

// goose resolves its own directories through etcetera's choose_app_strategy,
// which is XDG on macOS as well as Linux (#4267). The Apple layouts, which
// #3642 took for goose's own, and the older Block segment under XDG stay
// candidates, so a store under any of them is still read.
func TestGooseDataDirsCoverThePlatformAndTheOlderOnes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GOOSE_PATH_ROOT", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("DEJA_GOOSE_ROOT", "")
	t.Setenv("DEJA_GOOSE_DB", "")

	dirs := GooseDataDirs()
	if len(dirs) < 2 {
		t.Fatalf("candidates = %v, want the platform's and the older ones", dirs)
	}
	joined := strings.Join(dirs, "\n")
	xdg := filepath.Join(home, ".local", "share", "goose")
	if !strings.Contains(joined, xdg) {
		t.Errorf("the XDG location is not a candidate: %v", dirs)
	}
	// The older layout with the author segment, which tokscale reads as
	// "legacy Block/goose" — seen in the wild, so it stays a candidate.
	if !strings.Contains(joined, filepath.Join(home, ".local", "share", "Block", "goose")) {
		t.Errorf("the legacy Block/goose location is not a candidate: %v", dirs)
	}
	if runtime.GOOS == "darwin" {
		if !strings.Contains(joined, filepath.Join(home, "Library", "Application Support", "Block", "goose")) {
			t.Errorf("the Apple Block/goose location is not a candidate: %v", dirs)
		}
		if !strings.Contains(joined, filepath.Join(home, "Library", "Application Support", "goose")) {
			t.Errorf("the location without the Block segment is not a candidate: %v", dirs)
		}
	}
	if runtime.GOOS != "windows" && dirs[0] != xdg {
		t.Errorf("first candidate = %q, want %q", dirs[0], xdg)
	}

	// GOOSE_PATH_ROOT is the whole answer when it is set: goose puts config,
	// data and state under it and nothing stays behind.
	t.Setenv("GOOSE_PATH_ROOT", filepath.Join(home, "elsewhere"))
	if got := GooseDataDirs(); len(got) != 1 || got[0] != filepath.Join(home, "elsewhere", "data") {
		t.Errorf("with GOOSE_PATH_ROOT set, candidates = %v", got)
	}
}

// A store in any candidate root is read, and a store in two is read from both.
func TestGooseReadsEveryRootThatExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GOOSE_PATH_ROOT", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("DEJA_GOOSE_ROOT", "")
	t.Setenv("DEJA_GOOSE_DB", "")

	dirs := GooseDataDirs()
	// A legacy JSONL session in the last candidate — the one a single-root
	// reader on macOS would never have looked at.
	legacy := filepath.Join(dirs[len(dirs)-1], "sessions")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"id":"s-legacy","working_dir":"/work/api","description":"why the migration hangs",` +
		`"message_count":1,"created":"2026-07-17T09:00:00Z"}` + "\n" +
		`{"role":"user","created":1784278800,"content":[{"type":"text","text":"why does the migration hang"}]}` + "\n"
	if err := os.WriteFile(filepath.Join(legacy, "s-legacy.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	files := GooseSessionFiles()
	if len(files) != 1 || !strings.HasSuffix(files[0], "s-legacy.jsonl") {
		t.Fatalf("a session in a non-primary root is not a candidate: %v", files)
	}
	if n := len(LoadGoose()); n != 1 {
		t.Fatalf("sessions = %d, want the one in the non-primary root", n)
	}
	// And the file is claimed by the registry kind, so incremental ingest reads
	// it rather than treating it as unknown content.
	kind := ""
	for _, h := range Registry() {
		if h.Name != "goose" {
			continue
		}
		for _, k := range h.Kinds {
			if k.Match(files[0]) {
				kind = k.Name
			}
		}
	}
	if kind != "goose-jsonl" {
		t.Errorf("registry kind for %s = %q, want goose-jsonl", files[0], kind)
	}
}

// goose 1.46 on a mac keeps its sessions in ~/.local/share/goose: etcetera's
// choose_app_strategy is XDG on macOS too, the Apple layout being
// choose_native_strategy. GooseRoot is what install --auto and doctor key on,
// so it has to name the XDG root on a fresh mac, and the root that exists when
// only an older layout does (#4267).
func TestGooseRootIsTheOneGooseWritesOrTheOneThatExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", "")
	t.Setenv("GOOSE_PATH_ROOT", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("DEJA_GOOSE_ROOT", "")
	t.Setenv("DEJA_GOOSE_DB", "")

	if runtime.GOOS != "windows" {
		xdg := filepath.Join(home, ".local", "share", "goose")
		if got := GooseRoot(); got != xdg {
			t.Errorf("GooseRoot() with nothing on disk = %q, want %q", got, xdg)
		}
	}
	if runtime.GOOS == "darwin" {
		apple := filepath.Join(home, "Library", "Application Support", "Block", "goose")
		if err := os.MkdirAll(apple, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := GooseRoot(); got != apple {
			t.Errorf("GooseRoot() with only the Apple layout on disk = %q, want %q", got, apple)
		}
	}
	dirs := GooseDataDirs()
	last := dirs[len(dirs)-1]
	if err := os.MkdirAll(last, 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "darwin" {
		if got := GooseRoot(); got != last {
			t.Errorf("GooseRoot() with only %q on disk = %q", last, got)
		}
	}
}

// goose takes GOOSE_PATH_ROOT and XDG_DATA_HOME only when absolute
// (paths.rs validated_path_root, etcetera's env_var_or_none); a relative one is
// skipped for the default, not read against wherever deja runs (#4285).
func TestGooseDataDirsSkipARelativeRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("GOOSE_PATH_ROOT", filepath.Join("rel", "root"))
	for _, d := range GooseDataDirs() {
		if !filepath.IsAbs(d) {
			t.Errorf("a relative GOOSE_PATH_ROOT gave the candidate %q", d)
		}
	}
	t.Setenv("GOOSE_PATH_ROOT", "")
	t.Setenv("XDG_DATA_HOME", filepath.Join("rel", "data"))
	for _, d := range GooseDataDirs() {
		if !filepath.IsAbs(d) {
			t.Errorf("a relative XDG_DATA_HOME gave the candidate %q", d)
		}
	}
	if runtime.GOOS != "windows" {
		if want := filepath.Join(home, ".local", "share", "goose"); GooseDataDirs()[0] != want {
			t.Errorf("first candidate = %q, want the default %q", GooseDataDirs()[0], want)
		}
	}
}

// gooseStore writes a store the way goose writes one: sessions.updated_at in
// sqlite's CURRENT_TIMESTAMP format ("2026-07-27 15:29:47"), messages carrying
// unix seconds for the same instant. In real-world stores, .
func gooseStore(t *testing.T, db string, turns []struct {
	Session, Role, Text string
	At                  int64
}) {
	t.Helper()
	stmts := `create table if not exists sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table if not exists messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
`
	var stmtsSb23 strings.Builder
	for _, turn := range turns {
		fmt.Fprintf(&stmtsSb23,
			"insert or ignore into sessions values ('%[1]s','n','a goose session','/tmp/app',datetime(%[3]d,'unixepoch'),datetime(%[3]d,'unixepoch'));\n"+
				"update sessions set updated_at = datetime(%[3]d,'unixepoch') where id='%[1]s';\n"+
				"insert into messages (session_id,role,content_json,created_timestamp) values ('%[1]s','%[2]s',json_array(json_object('type','text','text','%[4]s')),%[3]d);\n",
			turn.Session, turn.Role, turn.At, turn.Text)
	}
	stmts += stmtsSb23.String()
	readerSQLite(t, db, stmts)
}

// A turn goose stored without a timestamp of its own is reachable only through
// its session, so the session-level half of the since clause is what finds it —
// and it has to compare two timestamps written in the same format. A real store
// keeps updated_at as sqlite's own "2026-07-27 15:29:47", which never compares
// greater than an RFC3339 string within the same day, so the turn was invisible
// to every incremental pass until the store was touched on a later date
// (#2030).
func TestGooseSinceStillFindsATurnWithNoTimestamp(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	db := filepath.Join(t.TempDir(), "sessions.db")
	type turn = struct {
		Session, Role, Text string
		At                  int64
	}
	first := int64(1785166187)
	gooseStore(t, db, []turn{{"g1", "user", "why does pgbouncer time out", first}})

	// A turn goose stored without a usable timestamp, on a session whose
	// updated_at moved a few minutes later — the same day.
	later := first + 600
	stmts := fmt.Sprintf(
		"insert into messages (session_id,role,content_json,created_timestamp) values ('g1','assistant',json_array(json_object('type','text','text','the pool is too small')),0);\n"+
			"update sessions set updated_at = datetime(%d,'unixepoch') where id='g1';\n", later)
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}

	got, err := ParseGooseDBSince(db, time.Unix(first, 0))
	if err != nil {
		t.Fatal(err)
	}
	// The turn itself, not just something: a clause that handed back the
	// session's other message and stopped would answer this with the wrong one.
	found := false
	for _, s := range got {
		for _, msg := range s.Messages {
			if strings.Contains(msg.Text, "the pool is too small") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("a turn with no timestamp of its own is reachable only through its session, and it did not come back")
	}
}

func gooseFixture(t *testing.T) (root, jsonl string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("DEJA_GOOSE_ROOT", filepath.Join(root, "goose"))
	dir := filepath.Join(root, "goose", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonl = filepath.Join(dir, "20250724_1.jsonl")
	return root, jsonl
}

const gooseJSONL = `{"description":"demo session","id":"20250724_1","created_at":"2026-07-24T10:00:00Z","updated_at":"2026-07-24T10:00:02Z","working_dir":"/workspace/demo","extension_data":{},"message_count":2}
{"id":"msg1","role":"user","created":1784278801,"content":[{"type":"text","text":"Say hello"}]}
{"id":"msg2","role":"assistant","created":1784278802,"content":[{"type":"text","text":"Hello from Goose!"}]}
`

func TestParseGooseFile(t *testing.T) {
	_, jsonl := gooseFixture(t)
	if err := os.WriteFile(jsonl, []byte(gooseJSONL), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGooseFile(jsonl)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "goose" || s.ID != "20250724_1" || s.Project != "workspace/demo" || s.Title != "demo session" {
		t.Fatalf("session = %#v", s)
	}
	if len(s.Messages) != 2 || s.Messages[0].Role != "user" || s.Messages[1].Text != "Hello from Goose!" {
		t.Fatalf("messages = %#v", s.Messages)
	}
}

func TestParseGooseFileFromOffset(t *testing.T) {
	_, jsonl := gooseFixture(t)
	head := gooseJSONL
	if err := os.WriteFile(jsonl, []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	off := int64(len(head))
	appendLine := `{"id":"msg3","role":"user","created":1784278803,"content":[{"type":"text","text":"follow up"}]}
`
	f, err := os.OpenFile(jsonl, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(appendLine); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGooseFileFromOffset(jsonl, off)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 1 || ss[0].Messages[0].Text != "follow up" {
		t.Fatalf("incremental = %#v, %v", ss, err)
	}
}

func TestParseGooseFileTornTail(t *testing.T) {
	_, jsonl := gooseFixture(t)
	torn := gooseJSONL + `{"id":"msg3","role":"user","cre`
	if err := os.WriteFile(jsonl, []byte(torn), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGooseFile(jsonl)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("torn tail should keep prior messages: %#v, %v", ss, err)
	}
}

func TestGooseSessionFiles(t *testing.T) {
	root, jsonl := gooseFixture(t)
	if err := os.WriteFile(jsonl, []byte(gooseJSONL), 0o644); err != nil {
		t.Fatal(err)
	}
	files := GooseSessionFiles()
	if len(files) != 1 || files[0] != jsonl {
		t.Fatalf("files = %#v", files)
	}
	db := filepath.Join(root, "goose", "sessions", "sessions.db")
	if err := os.WriteFile(db, []byte("sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	files = GooseSessionFiles()
	if len(files) != 2 {
		t.Fatalf("files with db = %#v", files)
	}
}

func TestParseGooseDB(t *testing.T) {
	if !SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("DEJA_GOOSE_ROOT", filepath.Join(root, "goose"))
	dir := filepath.Join(root, "goose", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "sessions.db")
	sql := `create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert into sessions values ('20250724_2','n','sqlite demo','/workspace/demo','2026-07-24T11:00:00Z','2026-07-24T11:00:02Z');
insert into messages values (1,'20250724_2','user','[{"type":"text","text":"db hello"}]',1784282401);
insert into messages values (2,'20250724_2','assistant','[{"type":"text","text":"db reply"}]',1784282402);`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("create db: %v: %s", err, out)
	}
	ss, err := ParseGooseDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "20250724_2" || len(ss[0].Messages) != 2 {
		t.Fatalf("db sessions = %#v, %v", ss, err)
	}
	if newest, err := ParseGooseNewest(db); err != nil || len(newest) != 1 {
		t.Fatalf("ParseGooseNewest = %v, %#v", err, newest)
	}
	if GooseStoreLacks(db, "20250724_2") || !GooseStoreLacks(db, "missing") {
		t.Fatalf("GooseStoreLacks unexpected result")
	}
}

// GOOSE_PATH_ROOT relocates config, data and state together. Ignoring it
// means indexing zero sessions for anyone who sets it.
func TestGooseDataDirFollowsPathRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GOOSE_PATH_ROOT", root)
	if got, want := GooseDataDir(), filepath.Join(root, "data"); got != want {
		t.Fatalf("GooseDataDir() = %q, want %q", got, want)
	}
	// It wins over the platform default and over XDG.
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "xdg"))
	if got, want := GooseDataDir(), filepath.Join(root, "data"); got != want {
		t.Fatalf("XDG took precedence: %q", got)
	}
}

// Goose stores subagent turns and cron-recipe runs in the same table as real
// sessions. Recall is about the user's own work, so those stay out; rows from
// before the column existed have no type and stay in, and so does every way a
// person reaches goose (#2873).
func TestParseGooseDBSkipsNonUserSessions(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "sessions.db")
	sql := `create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text, session_type text);
create table messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert into sessions values ('s_user','n','mine','/w','2026-07-24T11:00:00Z','2026-07-24T11:00:02Z','user');
insert into sessions values ('s_acp','n','acp','/w','2026-07-24T11:00:00Z','2026-07-24T11:00:02Z','acp');
insert into sessions values ('s_term','n','terminal','/w','2026-07-24T11:00:00Z','2026-07-24T11:00:02Z','terminal');
insert into sessions values ('s_gw','n','gateway','/w','2026-07-24T11:00:00Z','2026-07-24T11:00:02Z','gateway');
insert into sessions values ('s_hidden','n','no-session','/w','2026-07-24T11:00:00Z','2026-07-24T11:00:02Z','hidden');
insert into sessions values ('s_empty','n','no type at all','/w','2026-07-24T11:00:00Z','2026-07-24T11:00:02Z','');
insert into sessions values ('s_new','n','a type goose adds later','/w','2026-07-24T11:00:00Z','2026-07-24T11:00:02Z','somethingnew');
insert into sessions values ('s_old','n','legacy','/w','2026-07-24T11:00:00Z','2026-07-24T11:00:02Z',null);
insert into sessions values ('s_sub','n','subagent','/w','2026-07-24T11:00:00Z','2026-07-24T11:00:02Z','subagent');
insert into sessions values ('s_sub2','n','sub_agent','/w','2026-07-24T11:00:00Z','2026-07-24T11:00:02Z','sub_agent');
insert into sessions values ('s_cron','n','cron','/w','2026-07-24T11:00:00Z','2026-07-24T11:00:02Z','scheduled');
insert into messages values (1,'s_user','user','[{"type":"text","text":"mine"}]',1784282401);
insert into messages values (2,'s_acp','user','[{"type":"text","text":"through the editor"}]',1784282401);
insert into messages values (3,'s_term','user','[{"type":"text","text":"through the terminal"}]',1784282401);
insert into messages values (4,'s_gw','user','[{"type":"text","text":"through a chat app"}]',1784282401);
insert into messages values (5,'s_hidden','user','[{"type":"text","text":"a run that kept no session"}]',1784282401);
insert into messages values (6,'s_empty','user','[{"type":"text","text":"no type at all"}]',1784282401);
insert into messages values (7,'s_new','user','[{"type":"text","text":"a type goose has not shipped yet"}]',1784282401);
insert into messages values (8,'s_old','user','[{"type":"text","text":"legacy"}]',1784282401);
insert into messages values (9,'s_sub','user','[{"type":"text","text":"subagent noise"}]',1784282401);
insert into messages values (10,'s_sub2','user','[{"type":"text","text":"subagent noise"}]',1784282401);
insert into messages values (11,'s_cron','user','[{"type":"text","text":"cron noise"}]',1784282401);`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("create db: %v: %s", err, out)
	}
	ss, err := ParseGooseDB(db)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, s := range ss {
		got[s.ID] = true
	}
	// Everything a person did, whichever way they reached goose: the CLI, the
	// desktop app, and an editor speaking ACP are all the reader's own work.
	// `hidden` is `goose run --no-session`, which is somebody working too.
	// Named the other way round — an allow-list of "user" — a type goose added
	// later was dropped, and a reader's ACP sessions were missing from recall
	// with nothing saying why (#2873). The empty string and a type this build
	// has never heard of are in the keep list on purpose: what the filter must
	// never do again is drop work for carrying a name it was not told about.
	for _, id := range []string{"s_user", "s_old", "s_acp", "s_term", "s_gw", "s_hidden", "s_empty", "s_new"} {
		if !got[id] {
			t.Errorf("%s belongs in recall and is not there: %v", id, got)
		}
	}
	for _, id := range []string{"s_sub", "s_sub2", "s_cron"} {
		if got[id] {
			t.Errorf("%s is goose talking to itself and leaked into recall: %v", id, got)
		}
	}
}

func TestGooseEditCallIncludesReadsAndViews(t *testing.T) {
	for _, tc := range []struct {
		item map[string]any
		want string
	}{
		{
			item: map[string]any{"toolCall": map[string]any{"value": map[string]any{"name": "developer__read", "arguments": map[string]any{"path": "/w/a.go"}}}},
			want: "/w/a.go",
		},
		{
			item: map[string]any{"toolCall": map[string]any{"value": map[string]any{"name": "read_file", "arguments": map[string]any{"file_path": "/w/b.go"}}}},
			want: "/w/b.go",
		},
		{
			item: map[string]any{"toolCall": map[string]any{"value": map[string]any{"name": "developer__text_editor", "arguments": map[string]any{"command": "view", "path": "/w/c.go"}}}},
			want: "/w/c.go",
		},
		{
			item: map[string]any{"toolCall": map[string]any{"value": map[string]any{"name": "developer__text_editor", "arguments": map[string]any{"command": "undo_edit", "path": "/w/d.go"}}}},
			want: "",
		},
	} {
		if got := gooseEditCall(tc.item).FilePath; got != tc.want {
			t.Errorf("gooseEditCall(%v).FilePath = %q, want %q", tc.item, got, tc.want)
		}
	}
}
