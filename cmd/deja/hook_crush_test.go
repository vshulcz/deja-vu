package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// Crush fires only PreToolUse, so the fix pair for the command that failed
// before it comes out of crush.db and rides the next tool call, once.
func TestCrushToolHookCarriesThePreviousFailuresFixPair(t *testing.T) {
	if !sources.SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	tmp := seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	dir := os.Getenv("DEJA_INDEX_DIR")
	data := filepath.Join(tmp, "crushproj", ".crush")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(tmp, "crush-home")
	t.Setenv("DEJA_CRUSH_ROOT", home)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	reg, _ := json.Marshal(map[string]any{"projects": []map[string]string{{"path": "/work/app", "data_dir": data}}})
	if err := os.WriteFile(filepath.Join(home, "projects.json"), reg, 0o644); err != nil {
		t.Fatal(err)
	}
	parts := func(v any) string {
		b, _ := json.Marshal(v)
		return "'" + strings.ReplaceAll(string(b), "'", "''") + "'"
	}
	sql := `create table sessions (id text primary key, parent_session_id text, title text not null,
  message_count integer not null default 0, prompt_tokens integer not null default 0,
  completion_tokens integer not null default 0, cost real not null default 0.0,
  updated_at integer not null, created_at integer not null, summary_message_id text, todos text);
create table messages (id text primary key, session_id text not null, role text not null,
  parts text not null default '[]', model text, created_at integer not null, updated_at integer not null,
  finished_at integer, provider text, is_summary_message integer default 0 not null);
insert into sessions values ('c1',null,'t',3,0,0,0,102,100,null,null);
insert into messages values ('u1','c1','user',` + parts([]map[string]any{{"type": "text", "data": map[string]any{"text": "build it"}}}) + `,'m',100,100,null,'p',0);
insert into messages values ('a1','c1','assistant',` + parts([]map[string]any{{"type": "tool_call", "data": map[string]any{"id": "k1", "name": "bash", "input": `{"command":"make"}`}}}) + `,'m',101,101,null,'p',0);
insert into messages values ('r1','c1','tool',` + parts([]map[string]any{{"type": "tool_result", "data": map[string]any{"tool_call_id": "k1", "content": "goroutine 1 [running]:\npanic: sql: database is closed\nExit code 2\n<cwd>/work/app</cwd>"}}}) + `,'m',102,102,null,'p',0);
`
	cmd := exec.Command("sqlite3", filepath.Join(data, "crush.db"))
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build store: %v: %s", err, out)
	}
	tool := `{"event":"PreToolUse","session_id":"c1","cwd":"/work/app","tool_name":"view","tool_input":{"file_path":"/work/app/main.go"}}`
	got := runDeferredHook(t, dir, "hook-tool", []string{"--crush"}, tool)
	var resp struct {
		Version int    `json:"version"`
		Context string `json:"context"`
	}
	if err := json.Unmarshal([]byte(got), &resp); err != nil || resp.Version != 1 || !strings.Contains(resp.Context, "make clean && make CGO_ENABLED=0") {
		t.Fatalf("the next tool call did not carry the fix pair in Crush's shape: %q (%v)", got, err)
	}
	if again := runDeferredHook(t, dir, "hook-tool", []string{"--crush"}, tool); strings.Contains(again, "CGO_ENABLED=0") {
		t.Fatalf("the fix pair came twice: %q", again)
	}
}

// Crush has no compaction event. Summarising points the session row at a
// summary message and keeps the turns before it, so the next tool call finds
// the summary in crush.db and hands back the session as it stood, once.
func TestCrushSummaryIsCaughtUpAtTheNextToolCall(t *testing.T) {
	if !sources.SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	home := filepath.Join(t.TempDir(), "crush-home")
	t.Setenv("DEJA_CRUSH_ROOT", home)
	data := filepath.Join(workspace, ".crush")
	for _, d := range []string{home, data} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	reg, _ := json.Marshal(map[string]any{"projects": []map[string]string{{"path": workspace, "data_dir": data}}})
	if err := os.WriteFile(filepath.Join(home, "projects.json"), reg, 0o644); err != nil {
		t.Fatal(err)
	}
	text := func(s string) string {
		b, _ := json.Marshal([]map[string]any{{"type": "text", "data": map[string]any{"text": s}}})
		return "'" + strings.ReplaceAll(string(b), "'", "''") + "'"
	}
	sqlite := func(sql string) {
		cmd := exec.Command("sqlite3", filepath.Join(data, "crush.db"))
		cmd.Stdin = strings.NewReader(sql)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("store: %v: %s", err, out)
		}
	}
	sqlite(`create table sessions (id text primary key, parent_session_id text, title text not null,
  message_count integer not null default 0, prompt_tokens integer not null default 0,
  completion_tokens integer not null default 0, cost real not null default 0.0,
  updated_at integer not null, created_at integer not null, summary_message_id text, todos text);
create table messages (id text primary key, session_id text not null, role text not null,
  parts text not null default '[]', model text, created_at integer not null, updated_at integer not null,
  finished_at integer, provider text, is_summary_message integer default 0 not null);
insert into sessions values ('c2',null,'t',2,0,0,0,101,100,null,null);
insert into messages values ('u1','c2','user',` + text("fix the parser test") + `,'m',100,100,null,'p',0);
insert into messages values ('a1','c2','assistant',` + text("The parser test fails: want 3, got 4. I decided to fix parse.go next.") + `,'m',101,101,null,'p',0);
`)
	tool, _ := json.Marshal(map[string]any{"event": "PreToolUse", "session_id": "c2", "cwd": workspace,
		"tool_name": "view", "tool_input": map[string]any{"file_path": filepath.Join(workspace, "parse.go")}})
	if out := runDeferredHook(t, dir, "hook-tool", []string{"--crush"}, string(tool)); strings.Contains(out, "Compaction context") {
		t.Fatalf("a session never summarised got a packet: %s", out)
	}
	sqlite(`insert into messages values ('s1','c2','assistant',` + text("summary of the work so far") + `,'m',200,200,null,'p',1);
insert into messages values ('a2','c2','assistant',` + text("carrying on") + `,'m',201,201,null,'p',0);
update sessions set summary_message_id='s1', updated_at=201 where id='c2';
`)
	out := runDeferredHook(t, dir, "hook-tool", []string{"--crush"}, string(tool))
	for _, want := range []string{"Compaction context", "fix the parser test"} {
		if !strings.Contains(out, want) {
			t.Errorf("packet lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, "carrying on") {
		t.Errorf("the packet was built from turns after the summary: %s", out)
	}
	if again := runDeferredHook(t, dir, "hook-tool", []string{"--crush"}, string(tool)); strings.Contains(again, "Compaction context") {
		t.Fatalf("the same summary was handed back twice: %s", again)
	}
}
