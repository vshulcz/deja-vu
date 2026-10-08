package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func runDeferredHook(t *testing.T, dir, name string, rest []string, payload string) string {
	t.Helper()
	withHookStdin(t, payload)
	return captureStdout(t, func() { _ = runHookDeferred(dir, name, rest, commands[name]) })
}

// Kimi fires PostToolUseFailure and drops what it prints, so the fix pair for
// a failed command waits for the session's next prompt, the one event whose
// output reaches its model. The payload is Kimi's own: snake_case keys from
// toHookInputData, the failure under `error` (0.28.1).
func TestKimiFixPairRidesTheNextPrompt(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	dir := os.Getenv("DEJA_INDEX_DIR")
	failure, _ := json.Marshal(map[string]any{
		"hook_event_name": "PostToolUseFailure", "session_id": "kimi-1", "cwd": "/work/app",
		"tool_name": "Bash", "tool_input": map[string]any{"command": "make"}, "tool_call_id": "call_1",
		"error": map[string]any{"code": "TOOL_ERROR", "message": "goroutine 1 [running]:\npanic: sql: database is closed"},
	})
	if out := runDeferredHook(t, dir, "hook-tool-after", []string{"--defer"}, string(failure)); strings.TrimSpace(out) != "" {
		t.Fatalf("a deferred hook printed what Kimi would drop: %q", out)
	}
	prompt := func(id string) string {
		p, _ := json.Marshal(map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": id, "cwd": "/work/app", "prompt": "ok"})
		return runDeferredHook(t, dir, "hook-prompt", []string{"--plain"}, string(p))
	}
	if other := prompt("kimi-2"); strings.Contains(other, "CGO_ENABLED=0") {
		t.Fatalf("another session got this session's fix pair: %q", other)
	}
	got := prompt("kimi-1")
	if !strings.Contains(got, "make clean && make CGO_ENABLED=0") || strings.HasPrefix(strings.TrimSpace(got), "{") {
		t.Fatalf("the next prompt did not carry the fix pair as plain text: %q", got)
	}
	if again := prompt("kimi-1"); strings.Contains(again, "CGO_ENABLED=0") {
		t.Fatalf("the fix pair was delivered twice: %q", again)
	}
}

// Kiro's postToolUse output never reaches the model (2.28.0, measured), and an
// agent hook may name no session: the working directory stands in for it.
func TestKiroFixPairRidesTheNextPromptByDirectory(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	dir := os.Getenv("DEJA_INDEX_DIR")
	failure, _ := json.Marshal(map[string]any{
		"hook_event_name": "postToolUse", "cwd": "/work/app", "tool_name": "execute_bash",
		"tool_input":    map[string]any{"command": "make"},
		"tool_response": map[string]any{"exit_status": "1", "stderr": "goroutine 1 [running]:\npanic: sql: database is closed"},
	})
	if out := runDeferredHook(t, dir, "hook-tool-after", []string{"--defer"}, string(failure)); strings.TrimSpace(out) != "" {
		t.Fatalf("a deferred hook printed what Kiro would drop: %q", out)
	}
	p, _ := json.Marshal(map[string]any{"hook_event_name": "userPromptSubmit", "cwd": "/work/app", "prompt": "ok"})
	if got := runDeferredHook(t, dir, "hook-prompt", []string{"--plain"}, string(p)); !strings.Contains(got, "CGO_ENABLED=0") {
		t.Fatalf("the next prompt did not carry the fix pair: %q", got)
	}
}

// goose drops what its hooks print, and its failure event names no output; a
// Stop hook that blocks is the one answer it puts in front of the model, and
// the failure is in sessions.db by then. It blocks once per pair.
func TestGooseStopBlocksOnceWithTheFixPair(t *testing.T) {
	if !sources.SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	tmp := seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	dir := os.Getenv("DEJA_INDEX_DIR")
	root := filepath.Join(tmp, "goose-store")
	t.Setenv("DEJA_GOOSE_ROOT", root)
	if err := os.MkdirAll(filepath.Join(root, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	sql := `create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert into sessions values ('g1','n','d','/work/app','2026-10-07T10:00:00Z','2026-10-07T10:05:00Z');
insert into messages values (1,'g1','user','[{"type":"text","text":"build it"}]',100);
insert into messages values (2,'g1','assistant','[{"type":"toolRequest","id":"c","toolCall":{"status":"success","value":{"name":"developer__shell","arguments":{"command":"make"}}}}]',101);
insert into messages values (3,'g1','user','[{"type":"toolResponse","id":"c","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"goroutine 1 [running]:\npanic: sql: database is closed"}],"structuredContent":{"exit_code":2},"isError":true}}}]',102);`
	if out, err := exec.Command("sqlite3", filepath.Join(root, "sessions", "sessions.db"), sql).CombinedOutput(); err != nil {
		t.Fatalf("create db: %v: %s", err, out)
	}
	stop := `{"event":"Stop","session_id":"g1","working_dir":"/work/app"}`
	got := runDeferredHook(t, dir, "hook-stop", nil, stop)
	var resp struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(got), &resp); err != nil || resp.Decision != "block" || !strings.Contains(resp.Reason, "make clean && make CGO_ENABLED=0") {
		t.Fatalf("Stop did not block with the fix pair: %q (%v)", got, err)
	}
	if again := runDeferredHook(t, dir, "hook-stop", nil, stop); strings.TrimSpace(again) != "" {
		t.Fatalf("Stop blocked twice for one pair: %q", again)
	}
}

// goose has no compaction event. Compacting keeps the old messages, hidden
// from the agent, and adds an agent-only note, so the turn's Stop finds the
// compaction in sessions.db and hands the packet back once.
func TestGooseCompactionIsCaughtUpAtStop(t *testing.T) {
	if !sources.SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	root := filepath.Join(t.TempDir(), "goose-store")
	t.Setenv("DEJA_GOOSE_ROOT", root)
	if err := os.MkdirAll(filepath.Join(root, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(root, "sessions", "sessions.db")
	sqlite := func(sql string) {
		if out, err := exec.Command("sqlite3", db, sql).CombinedOutput(); err != nil {
			t.Fatalf("store: %v: %s", err, out)
		}
	}
	ws := strings.ReplaceAll(workspace, "'", "''")
	sqlite(`create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer, metadata_json text);
insert into sessions values ('g2','n','d','` + ws + `','2026-10-07T10:00:00Z','2026-10-07T10:05:00Z');
insert into messages values (1,'g2','user','[{"type":"text","text":"fix the parser test"}]',100,'{"userVisible":true,"agentVisible":true}');
insert into messages values (2,'g2','assistant','[{"type":"text","text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}]',101,'{"userVisible":true,"agentVisible":true}');`)
	stop := `{"event":"Stop","session_id":"g2","working_dir":` + strconvQuoteJSON(workspace) + `}`
	if out := runDeferredHook(t, dir, "hook-stop", nil, stop); strings.Contains(out, "Compaction context") {
		t.Fatalf("a session that never compacted got a packet: %s", out)
	}
	sqlite(`delete from messages;
insert into messages values (1,'g2','user','[{"type":"text","text":"fix the parser test"}]',100,'{"userVisible":true,"agentVisible":false}');
insert into messages values (2,'g2','assistant','[{"type":"text","text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}]',101,'{"userVisible":true,"agentVisible":false}');
insert into messages values (3,'g2','user','[{"type":"text","text":"summary of the work so far"}]',200,'{"userVisible":false,"agentVisible":true}');
insert into messages values (4,'g2','assistant','[{"type":"text","text":"Your context was compacted. The previous message contains a summary of the conversation so far."}]',200,'{"userVisible":false,"agentVisible":true}');
insert into messages values (5,'g2','assistant','[{"type":"text","text":"carrying on"}]',201,'{"userVisible":true,"agentVisible":true}');`)
	got := runDeferredHook(t, dir, "hook-stop", nil, stop)
	var resp struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(got), &resp); err != nil || resp.Decision != "block" {
		t.Fatalf("Stop did not block with the packet: %q (%v)", got, err)
	}
	for _, want := range []string{"Compaction context", "fix the parser test"} {
		if !strings.Contains(resp.Reason, want) {
			t.Errorf("packet lacks %q: %s", want, resp.Reason)
		}
	}
	if strings.Contains(resp.Reason, "carrying on") || strings.Contains(resp.Reason, "summary of the work") {
		t.Errorf("the packet holds turns from after the compaction: %s", resp.Reason)
	}
	if again := runDeferredHook(t, dir, "hook-stop", nil, stop); strings.TrimSpace(again) != "" {
		t.Fatalf("Stop blocked twice for one compaction: %q", again)
	}
}

func strconvQuoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestWithDeferredKeepsTheAnswersShape(t *testing.T) {
	const pending = "waited"
	cases := []struct {
		name, out string
		plain     bool
		want      []string
	}{
		{"empty envelope", "", false, []string{`"hookEventName":"PreToolUse"`, "waited"}},
		{"empty plain", "", true, []string{"waited"}},
		{"plain own", "own line\n", true, []string{"waited\n\nown line"}},
		{"envelope own", `{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"own"}}`, false, []string{`waited\n\nown`}},
		{"flat", `{"additionalContext":"own"}`, false, []string{`waited\n\nown`}},
		{"spawn rewrite", `{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":{"prompt":"x"}}}`, false, []string{`"updatedInput"`, "waited"}},
		{"receipt only", `{"systemMessage":"note"}`, false, []string{`"systemMessage":"note"`, `"additionalContext":"waited"`}},
	}
	for _, c := range cases {
		got := string(withDeferred([]byte(c.out), pending, c.plain, "PreToolUse"))
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q lacks %q", c.name, got, w)
			}
		}
	}
	if got := string(withDeferred([]byte("x"), "", false, "PreToolUse")); got != "x" {
		t.Errorf("nothing pending changed the answer: %q", got)
	}
}

func TestDeferredReplacesTheOldPromptAndAddsUpFailures(t *testing.T) {
	dir := t.TempDir()
	key := deferredKey("s1", "")
	deferText(dir, key, "hook-prompt", "first answer")
	deferText(dir, key, "hook-prompt", "second answer")
	deferText(dir, key, "hook-tool-after", "pair one")
	deferText(dir, key, "hook-tool-after", "pair two")
	deferText(dir, key, "hook-context", "digest")
	got := takeDeferred(dir, key)
	if strings.Contains(got, "first answer") || !strings.Contains(got, "second answer") {
		t.Errorf("an older prompt's answer survived a newer one: %q", got)
	}
	if !strings.Contains(got, "pair one") || !strings.Contains(got, "pair two") {
		t.Errorf("a failure pair was dropped: %q", got)
	}
	if !strings.HasPrefix(got, "digest") {
		t.Errorf("the digest does not lead: %q", got)
	}
	if again := takeDeferred(dir, key); again != "" {
		t.Errorf("taken twice: %q", again)
	}
	if hasDeferredFor(dir, key) {
		t.Error("the session's directory outlived the take")
	}
}

func TestDeferredExpires(t *testing.T) {
	dir := t.TempDir()
	key := deferredKey("s1", "")
	deferText(dir, key, "hook-prompt", "stale answer")
	files, _ := filepath.Glob(filepath.Join(deferredRoot(dir), key, "1prompt-*"))
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	old := time.Now().Add(-deferredTTL - time.Minute)
	if err := os.Chtimes(files[0], old, old); err != nil {
		t.Fatal(err)
	}
	if got := takeDeferred(dir, key); got != "" {
		t.Errorf("a stale answer was delivered: %q", got)
	}
}

func TestAStaleSessionIsSweptByTheNextDefer(t *testing.T) {
	dir := t.TempDir()
	gone := deferredKey("gone", "")
	deferText(dir, gone, "hook-tool", "line for a session that never came back")
	old := time.Now().Add(-deferredTTL - time.Minute)
	p := filepath.Join(deferredRoot(dir), gone)
	files, _ := filepath.Glob(filepath.Join(p, "*"))
	for _, f := range append(files, p) {
		if err := os.Chtimes(f, old, old); err != nil {
			t.Fatal(err)
		}
	}
	live := deferredKey("live", "")
	deferText(dir, live, "hook-tool", "fresh line")
	if hasDeferredFor(dir, gone) {
		t.Error("a session past the TTL kept its directory")
	}
	if !hasDeferredFor(dir, live) {
		t.Error("the fresh session lost its line")
	}
}
