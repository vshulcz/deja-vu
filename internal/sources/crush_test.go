package sources

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const crushSchema = `create table sessions (
  id text primary key, parent_session_id text, title text not null,
  message_count integer not null default 0, prompt_tokens integer not null default 0,
  completion_tokens integer not null default 0, cost real not null default 0.0,
  updated_at integer not null, created_at integer not null,
  summary_message_id text, todos text);
create table messages (
  id text primary key, session_id text not null, role text not null,
  parts text not null default '[]', model text,
  created_at integer not null, updated_at integer not null,
  finished_at integer, provider text, is_summary_message integer default 0 not null);
`

// crushStore builds a store where Crush puts one: under the project it belongs
// to. The depth is the attribution, so a helper that flattened it would test a
// path the parser never sees.
func crushStore(t *testing.T, project, sql string) string {
	t.Helper()
	if !SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), project, ".crush", "crush.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sqlite3", db)
	cmd.Stdin = strings.NewReader(crushSchema + sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build store: %v: %s", err, out)
	}
	return db
}

func crushParts(t *testing.T, parts any) string {
	t.Helper()
	b, err := json.Marshal(parts)
	if err != nil {
		t.Fatal(err)
	}
	// Through the JSON encoder rather than pasted: a hand-written literal with
	// a quote or a backslash in it is a broken SQL string, not a test.
	return "'" + strings.ReplaceAll(string(b), "'", "''") + "'"
}

func crushInsert(t *testing.T, id, session, role string, at int64, parts any) string {
	t.Helper()
	return "insert into messages values ('" + id + "','" + session + "','" + role + "'," +
		crushParts(t, parts) + ",'m'," + itoa(int(at)) + "," + itoa(int(at)) + ",null,'p',0);\n"
}

// The whole read, end to end: speech under the speaker, the shell command as a
// command record, what the tool printed as tool output, and the edited path as
// a file record. Each of the four is a different surface — search, `how`,
// error matching and `blame` — and a parser that drops one is silent there.
func TestCrushReadsEveryRoleOutOfOneSession(t *testing.T) {
	sql := "insert into sessions values ('s1',null,'ship the fix',4,0,0,0.0,1784282403,1784282400,null,null);\n"
	sql += crushInsert(t, "m1", "s1", "user", 1784282400, []any{
		map[string]any{"type": "text", "data": map[string]any{"text": "run the build"}},
		map[string]any{"type": "finish", "data": map[string]any{"reason": "stop"}},
	})
	sql += crushInsert(t, "m2", "s1", "assistant", 1784282401, []any{
		map[string]any{"type": "tool_call", "data": map[string]any{
			"id": "call_0", "name": "bash",
			"input": `{"command": "go test ./internal/zibblex", "description": "run the build"}`,
		}},
	})
	sql += crushInsert(t, "m3", "s1", "tool", 1784282402, []any{
		map[string]any{"type": "tool_result", "data": map[string]any{
			"tool_call_id": "call_0", "name": "bash",
			"content": "ok  zibblex 0.4s\n\n<cwd>/workspace/demo</cwd>",
		}},
	})
	sql += crushInsert(t, "m4", "s1", "assistant", 1784282403, []any{
		map[string]any{"type": "tool_call", "data": map[string]any{
			"id": "call_1", "name": "edit",
			"input": `{"file_path": "/workspace/demo/zibblex.go", "old_string": "a", "new_string": "b"}`,
		}},
	})

	ss, err := ParseCrushDB(crushStore(t, "demo", sql))
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "crush" || s.ID != "s1" || s.Title != "ship the fix" {
		t.Fatalf("session header: %#v", s)
	}
	// The project is the directory the store sits under, not ".crush".
	if s.Project != "demo" {
		t.Fatalf("project = %q, want demo", s.Project)
	}
	got := map[string]string{}
	for _, m := range s.Messages {
		got[m.Role] = m.Text
	}
	for _, want := range []struct{ role, text string }{
		{"user", "run the build"},
		{RoleCommand, "go test ./internal/zibblex"},
		{RoleToolOutput, "ok  zibblex 0.4s"},
		{RoleFiles, "/workspace/demo/zibblex.go"},
	} {
		if strings.TrimSpace(got[want.role]) != want.text {
			t.Errorf("%s = %q, want %q (all: %#v)", want.role, got[want.role], want.text, got)
		}
	}
	// The <cwd> tag is the same directory on every tool result in the store;
	// left in, a search for the project name matches every one of them.
	if strings.Contains(got[RoleToolOutput], "cwd") {
		t.Errorf("the cwd tag reached the index: %q", got[RoleToolOutput])
	}
}

// Crush's schema comments both stamp columns as milliseconds and the release
// writes whole seconds. Read as milliseconds a seconds row lands in 1970, and
// read as seconds a millisecond row lands in the year 58000 — which takes the
// top of every recency surface and keeps it.
func TestCrushReadsSecondsAndMillisecondStamps(t *testing.T) {
	const wantYear = 2026
	for _, unit := range []struct {
		name string
		at   int64
	}{{"seconds", 1784282400}, {"milliseconds", 1784282400000}} {
		t.Run(unit.name, func(t *testing.T) {
			sql := "insert into sessions values ('s1',null,'t',1,0,0,0.0," + itoa(int(unit.at)) + "," + itoa(int(unit.at)) + ",null,null);\n"
			sql += crushInsert(t, "m1", "s1", "user", unit.at, []any{
				map[string]any{"type": "text", "data": map[string]any{"text": "hello"}},
			})
			ss, err := ParseCrushDB(crushStore(t, "demo", sql))
			if err != nil || len(ss) != 1 {
				t.Fatalf("parse: %v %d", err, len(ss))
			}
			if y := ss[0].Messages[0].Time.Year(); y != wantYear {
				t.Fatalf("%s row dated %d, want %d", unit.name, y, wantYear)
			}
		})
	}
}

// A since-pass has to come back with the session that moved and skip the one
// that did not; the watermark and the row are in different units, which is the
// bug this guards (#2064).
func TestCrushSincePassSkipsWhatDidNotMove(t *testing.T) {
	old, recent := int64(1684282400), int64(1784282400)
	sql := "insert into sessions values ('old',null,'old',1,0,0,0.0," + itoa(int(old)) + "," + itoa(int(old)) + ",null,null);\n"
	sql += "insert into sessions values ('new',null,'new',1,0,0,0.0," + itoa(int(recent)) + "," + itoa(int(recent)) + ",null,null);\n"
	sql += crushInsert(t, "m1", "old", "user", old, []any{
		map[string]any{"type": "text", "data": map[string]any{"text": "stale turn"}},
	})
	sql += crushInsert(t, "m2", "new", "user", recent, []any{
		map[string]any{"type": "text", "data": map[string]any{"text": "fresh turn"}},
	})
	db := crushStore(t, "demo", sql)

	all, err := ParseCrushDB(db)
	if err != nil || len(all) != 2 {
		t.Fatalf("full pass: %v, %d sessions", err, len(all))
	}
	since, err := ParseCrushDBSince(db, time.Unix(recent-60, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(since) != 1 || since[0].ID != "new" {
		t.Fatalf("since pass = %#v, want only the session that moved", since)
	}
}

// Crush spawns subagent sessions, and the row naming the parent is the only
// place that edge exists. Without it a subagent's turns look like a separate
// piece of work by someone else.
func TestCrushKeepsTheSubagentEdge(t *testing.T) {
	sql := "insert into sessions values ('parent',null,'top',1,0,0,0.0,1784282400,1784282400,null,null);\n"
	sql += "insert into sessions values ('child','parent','sub',1,0,0,0.0,1784282401,1784282401,null,null);\n"
	sql += crushInsert(t, "m1", "parent", "user", 1784282400, []any{
		map[string]any{"type": "text", "data": map[string]any{"text": "delegate it"}},
	})
	sql += crushInsert(t, "m2", "child", "assistant", 1784282401, []any{
		map[string]any{"type": "text", "data": map[string]any{"text": "on it"}},
	})
	ss, err := ParseCrushDB(crushStore(t, "demo", sql))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range ss {
		switch s.ID {
		case "parent":
			if s.Kind == "subagent" || s.Parent != "" {
				t.Errorf("the top-level session was filed as a subagent: %#v", s)
			}
		case "child":
			if s.Kind != "subagent" || s.Parent != "parent" {
				t.Errorf("subagent edge lost: kind=%q parent=%q", s.Kind, s.Parent)
			}
		}
	}
}

// The parts column is JSON inside a database, so the corruption sweep over file
// fixtures never reaches it. A NUL or a bad byte that survives here reaches
// every consumer that forwards Text without the display layer's sanitiser
// (#1740).
func TestCrushDamagedPartsDoNotReachTheIndex(t *testing.T) {
	sql := "insert into sessions values ('s1',null,'t',3,0,0,0.0,1784282400,1784282400,null,null);\n"
	// A real NUL and a lone surrogate escape, both of which a store can hold
	// and neither of which is text.
	sql += "insert into messages values ('m1','s1','user','[{\"type\":\"text\",\"data\":{\"text\":\"before\\u0000after\"}}]','m',1784282400,1784282400,null,'p',0);\n"
	sql += "insert into messages values ('m2','s1','assistant','[{\"type\":\"text\",\"data\":{\"text\":\"lone \\ud800 half\"}}]','m',1784282401,1784282401,null,'p',0);\n"
	// And a parts column that is not JSON at all: a half-written row is
	// ordinary, and it must cost that message rather than the session.
	sql += "insert into messages values ('m3','s1','assistant','[{\"type\":\"tex','m',1784282402,1784282402,null,'p',0);\n"
	sql += crushInsert(t, "m4", "s1", "assistant", 1784282403, []any{
		map[string]any{"type": "text", "data": map[string]any{"text": "the readable turn"}},
	})

	ss, err := ParseCrushDB(crushStore(t, "demo", sql))
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1 — a damaged row cost the session", len(ss))
	}
	readable := false
	for _, m := range ss[0].Messages {
		if !utf8.ValidString(m.Text) {
			t.Errorf("invalid utf-8 reached the index: %q", m.Text)
		}
		if strings.ContainsRune(m.Text, 0) {
			t.Errorf("a NUL reached the index: %q", m.Text)
		}
		if m.Text == "the readable turn" {
			readable = true
		}
	}
	if !readable {
		t.Error("the undamaged turn did not come back")
	}
}

// The registry is what makes the stores findable — nothing under the data home
// holds a transcript. An entry for a project that has since been deleted is
// ordinary, and must not become an error or an empty row.
func TestCrushDBsComeFromTheRegistryAndSkipWhatIsGone(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CRUSH_ROOT", root)

	live := filepath.Join(t.TempDir(), "live", ".crush")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "crush.db"), []byte("SQLite format 3\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	// One with data_dir spelled out, one with only a path — Crush writes both
	// — and one that no longer exists.
	byPath := filepath.Join(t.TempDir(), "bypath")
	if err := os.MkdirAll(filepath.Join(byPath, ".crush"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(byPath, ".crush", "crush.db"), []byte("SQLite format 3\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{"projects": []any{
		map[string]any{"path": filepath.Dir(live), "data_dir": live},
		map[string]any{"path": byPath},
		map[string]any{"path": filepath.Join(root, "gone"), "data_dir": filepath.Join(root, "gone", ".crush")},
	}}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "projects.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}

	got := CrushDBs()
	if len(got) != 2 {
		t.Fatalf("CrushDBs() = %v, want the two stores that exist", got)
	}
	if got[0] != filepath.Join(live, "crush.db") {
		t.Errorf("data_dir entry: %q", got[0])
	}
	if got[1] != filepath.Join(byPath, ".crush", "crush.db") {
		t.Errorf("path-only entry did not fall back to <project>/.crush: %q", got[1])
	}

	// No registry at all is a machine without Crush, not an error.
	t.Setenv("DEJA_CRUSH_ROOT", filepath.Join(root, "nowhere"))
	if got := CrushDBs(); len(got) != 0 {
		t.Fatalf("a machine with no registry listed %v", got)
	}
}
