package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func execSQLFixture(t *testing.T, db, script string) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
}

func TestOpencodeV1PopulatesAgentModelTokensAndToolStats(t *testing.T) {
	db := filepath.Join(t.TempDir(), "opencode.db")
	execSQLFixture(t, db, `
create table session (id text primary key, parent_id text, title text, directory text, time_created int, time_updated int);
create table message (id text primary key, session_id text, time_created int, time_updated int, data text);
create table part (id text primary key, message_id text, session_id text, time_created int, time_updated int, data text);

insert into session values ('ses_root', null, 'Root task', '/work/app', 1700000000000, 1700000010000);
insert into session values ('ses_child', 'ses_root', 'Child task', '/work/app', 1700000002000, 1700000009000);

insert into message values ('m1', 'ses_root', 1700000001000, 1700000001000,
	'{"role":"user","agent":"sisyphus","model":{"providerID":"anthropic","modelID":"claude-opus-4-6"}}');
insert into part values ('p1', 'm1', 'ses_root', 1700000001000, 1700000001000,
	'{"type":"text","text":"audit the retry queue"}');

insert into message values ('m2', 'ses_root', 1700000003000, 1700000005000,
	'{"role":"assistant","agent":"sisyphus","providerID":"anthropic","modelID":"claude-opus-4-6","tokens":{"input":120,"output":45,"reasoning":15,"cache":{"read":300}}}');
insert into part values ('p2', 'm2', 'ses_root', 1700000003000, 1700000003000,
	'{"type":"tool","tool":"read","state":{"status":"completed","input":{"filePath":"/work/app/queue.go"}}}');
insert into part values ('p3', 'm2', 'ses_root', 1700000004000, 1700000004000,
	'{"type":"tool","tool":"glob","state":{"status":"completed","input":{"pattern":"*.go"}}}');
insert into part values ('p4', 'm2', 'ses_root', 1700000004500, 1700000004500,
	'{"type":"tool","tool":"bash","state":{"status":"completed","input":{"command":"go test ./..."},"output":"FAIL","metadata":{"exit":1}}}');
insert into part values ('p5', 'm2', 'ses_root', 1700000004800, 1700000004800,
	'{"type":"tool","tool":"edit","state":{"status":"error","input":{"filePath":"/work/app/queue.go","oldString":"a","newString":"b"}}}');
insert into part values ('p6', 'm2', 'ses_root', 1700000005000, 1700000005000,
	'{"type":"text","text":"fixed the queue"}');

insert into message values ('m3', 'ses_child', 1700000006000, 1700000008000,
	'{"role":"assistant","agent":"explore","providerID":"openai","modelID":"gpt-5.4","tokens":{"input":50,"output":20,"reasoning":5,"cache":{"read":10}}}');
insert into part values ('p7', 'm3', 'ses_child', 1700000006000, 1700000008000,
	'{"type":"text","text":"found the caller"}');
`)

	ss, err := ParseOpencodeDB(db)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]model.Session{}
	for _, s := range ss {
		byID[s.ID] = s
	}
	root, ok := byID["ses_root"]
	if !ok {
		t.Fatalf("missing ses_root in %+v", ss)
	}
	if root.Agent != "sisyphus" {
		t.Errorf("root.Agent = %q, want sisyphus", root.Agent)
	}
	wantModel := &model.Model{Provider: "anthropic", ID: "claude-opus-4-6"}
	if !reflect.DeepEqual(root.Model, wantModel) {
		t.Errorf("root.Model = %+v, want %+v", root.Model, wantModel)
	}
	// m2 has 5 parts but its token usage must be counted once per message.
	wantTokens := &model.Tokens{Input: 120, Output: 45, Reasoning: 15, CacheRead: 300}
	if !reflect.DeepEqual(root.Tokens, wantTokens) {
		t.Errorf("root.Tokens = %+v, want %+v", root.Tokens, wantTokens)
	}
	wantTools := map[string]int{"read": 1, "glob": 1, "bash": 1, "edit": 1}
	if !reflect.DeepEqual(root.ToolCalls, wantTools) {
		t.Errorf("root.ToolCalls = %+v, want %+v", root.ToolCalls, wantTools)
	}
	if root.ToolErrors != 2 {
		t.Errorf("root.ToolErrors = %d, want 2 (bash exit 1 + edit status error)", root.ToolErrors)
	}

	child, ok := byID["ses_child"]
	if !ok {
		t.Fatalf("missing ses_child in %+v", ss)
	}
	if child.Kind != "subagent" || child.Parent != "ses_root" || child.Agent != "explore" {
		t.Errorf("child lineage = (%q, %q, %q), want (subagent, ses_root, explore)", child.Kind, child.Parent, child.Agent)
	}
	if !reflect.DeepEqual(child.Model, &model.Model{Provider: "openai", ID: "gpt-5.4"}) {
		t.Errorf("child.Model = %+v", child.Model)
	}
	if !reflect.DeepEqual(child.Tokens, &model.Tokens{Input: 50, Output: 20, Reasoning: 5, CacheRead: 10}) {
		t.Errorf("child.Tokens = %+v", child.Tokens)
	}
}

func TestOpencodeV2PopulatesAgentModelTokensAndToolStats(t *testing.T) {
	db := filepath.Join(t.TempDir(), "opencode.db")
	execSQLFixture(t, db, `
create table session_v2 (id text primary key, parent_id text, title text, directory text, model text, time_created int, time_updated int);
create table session_message (id text primary key, session_id text, type text, seq int, time_created int, time_updated int, data text);

insert into session_v2 values ('s_v2', 's_parent', 'V2 subagent', '/work/v2', '{"providerID":"anthropic","modelID":"claude-sonnet-4-6"}', 1700000000000, 1700000010000);
insert into session_message values ('sm1', 's_v2', 'user', 0, 1700000001000, 1700000001000,
	'{"text":"check v2 store"}');
insert into session_message values ('sm2', 's_v2', 'assistant', 1, 1700000002000, 1700000004000,
	'{"agent":"librarian","tokens":{"input":80,"output":30,"reasoning":10,"cache":{"read":20}},"content":[{"type":"tool","name":"grep","state":{"status":"completed","input":{"pattern":"foo"}}},{"type":"tool","name":"shell","state":{"status":"completed","input":{"command":"go test ./..."},"structured":{"exit":1},"content":[{"type":"text","text":"FAIL"}]}},{"type":"text","text":"checked"}]}');
`)

	ss, err := ParseOpencodeDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("got %d sessions, want 1", len(ss))
	}
	s := ss[0]
	if s.Kind != "subagent" || s.Parent != "s_parent" || s.Agent != "librarian" {
		t.Errorf("lineage = (%q, %q, %q), want (subagent, s_parent, librarian)", s.Kind, s.Parent, s.Agent)
	}
	if !reflect.DeepEqual(s.Model, &model.Model{Provider: "anthropic", ID: "claude-sonnet-4-6"}) {
		t.Errorf("s.Model = %+v", s.Model)
	}
	if !reflect.DeepEqual(s.Tokens, &model.Tokens{Input: 80, Output: 30, Reasoning: 10, CacheRead: 20}) {
		t.Errorf("s.Tokens = %+v", s.Tokens)
	}
	if !reflect.DeepEqual(s.ToolCalls, map[string]int{"grep": 1, "shell": 1}) {
		t.Errorf("s.ToolCalls = %+v", s.ToolCalls)
	}
	if s.ToolErrors != 1 {
		t.Errorf("s.ToolErrors = %d, want 1", s.ToolErrors)
	}
}

func TestClaudeParserDeduplicatesMultiBlockUsageAndCountsTools(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	lines := []string{
		`{"type":"user","sessionId":"s1","isSidechain":true,"agentId":"sub1","attributionAgent":"Explore","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"find the handler"}}`,
		// Two JSONL lines for the same API call (requestId req_1 / message.id msg_1), repeating usage.
		`{"type":"assistant","sessionId":"s1","isSidechain":true,"agentId":"sub1","attributionAgent":"Explore","requestId":"req_1","timestamp":"2026-01-02T03:04:06Z","message":{"id":"msg_1","model":"claude-opus-4-6","role":"assistant","usage":{"input_tokens":100,"output_tokens":40,"cache_read_input_tokens":500,"output_tokens_details":{"thinking_tokens":12}},"content":[{"type":"text","text":"looking"}]}}`,
		`{"type":"assistant","sessionId":"s1","isSidechain":true,"agentId":"sub1","attributionAgent":"Explore","requestId":"req_1","timestamp":"2026-01-02T03:04:07Z","message":{"id":"msg_1","model":"claude-opus-4-6","role":"assistant","usage":{"input_tokens":100,"output_tokens":40,"cache_read_input_tokens":500,"output_tokens_details":{"thinking_tokens":12}},"content":[{"type":"tool_use","id":"tu_1","name":"Read","input":{"file_path":"/w/a.go"}},{"type":"tool_use","id":"tu_2","name":"Bash","input":{"command":"go test ./..."}}]}}`,
		`{"type":"user","sessionId":"s1","isSidechain":true,"agentId":"sub1","attributionAgent":"Explore","timestamp":"2026-01-02T03:04:08Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_2","is_error":true,"content":"Exit code 1\nFAIL"}]}}`,
		// Second API call (req_2).
		`{"type":"assistant","sessionId":"s1","isSidechain":true,"agentId":"sub1","attributionAgent":"Explore","requestId":"req_2","timestamp":"2026-01-02T03:04:09Z","message":{"id":"msg_2","model":"claude-opus-4-6","role":"assistant","usage":{"input_tokens":50,"output_tokens":20,"cache_read_input_tokens":200},"content":[{"type":"text","text":"done"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClaudeFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ParseClaudeFile = %+v, %v", ss, err)
	}
	s := ss[0]
	if s.ID != "sub1" || s.Kind != "sidechain" || s.Parent != "s1" || s.Agent != "Explore" {
		t.Errorf("identity = (%q, %q, %q, %q)", s.ID, s.Kind, s.Parent, s.Agent)
	}
	if !reflect.DeepEqual(s.Model, &model.Model{Provider: "anthropic", ID: "claude-opus-4-6"}) {
		t.Errorf("s.Model = %+v", s.Model)
	}
	wantTokens := &model.Tokens{Input: 150, Output: 60, Reasoning: 12, CacheRead: 700}
	if !reflect.DeepEqual(s.Tokens, wantTokens) {
		t.Errorf("s.Tokens = %+v, want %+v", s.Tokens, wantTokens)
	}
	wantTools := map[string]int{"Read": 1, "Bash": 1}
	if !reflect.DeepEqual(s.ToolCalls, wantTools) {
		t.Errorf("s.ToolCalls = %+v, want %+v", s.ToolCalls, wantTools)
	}
	if s.ToolErrors != 1 {
		t.Errorf("s.ToolErrors = %d, want 1", s.ToolErrors)
	}
}

func TestCodexRolloutPopulatesAgentModelTokensAndToolStats(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-2026-01-02T03-04-05-child-1.jsonl")
	lines := []string{
		`{"timestamp":"2026-01-02T03:04:05Z","type":"session_meta","payload":{"id":"child-1","cwd":"/work/codex","model_provider":"openai","source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent-1","depth":1}}}}}`,
		`{"timestamp":"2026-01-02T03:04:06Z","type":"turn_context","payload":{"model":"gpt-5.4","collaboration_mode":{"mode":"code","settings":{"model":"gpt-5.4"}}}}`,
		`{"timestamp":"2026-01-02T03:04:07Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"run tests"}]}}`,
		`{"timestamp":"2026-01-02T03:04:08Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{\"cmd\":\"go test ./...\"}"}}`,
		`{"timestamp":"2026-01-02T03:04:09Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"Process exited with code 1\nOutput:\nFAIL"}}`,
		`{"timestamp":"2026-01-02T03:04:10Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"c2","input":"*** Begin Patch\n*** Update File: a.go\n@@\n-old\n+new\n*** End Patch"}}`,
		// Duplicate total_tokens event is ignored; only distinct totals accumulate.
		`{"timestamp":"2026-01-02T03:04:11Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":1000},"last_token_usage":{"input_tokens":800,"cached_input_tokens":600,"output_tokens":150,"reasoning_output_tokens":50}}}}`,
		`{"timestamp":"2026-01-02T03:04:11Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":1000},"last_token_usage":{"input_tokens":800,"cached_input_tokens":600,"output_tokens":150,"reasoning_output_tokens":50}}}}`,
		`{"timestamp":"2026-01-02T03:04:12Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"patched a.go"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodexRollout(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ParseCodexRollout = %+v, %v", ss, err)
	}
	s := ss[0]
	if s.ID != "child-1" || s.Kind != "subagent" || s.Parent != "parent-1" || s.Agent != "code" {
		t.Errorf("lineage = (%q, %q, %q, %q)", s.ID, s.Kind, s.Parent, s.Agent)
	}
	if !reflect.DeepEqual(s.Model, &model.Model{Provider: "openai", ID: "gpt-5.4"}) {
		t.Errorf("s.Model = %+v", s.Model)
	}
	wantTokens := &model.Tokens{Input: 800, Output: 150, Reasoning: 50, CacheRead: 600}
	if !reflect.DeepEqual(s.Tokens, wantTokens) {
		t.Errorf("s.Tokens = %+v, want %+v", s.Tokens, wantTokens)
	}
	wantTools := map[string]int{"exec_command": 1, "apply_patch": 1}
	if !reflect.DeepEqual(s.ToolCalls, wantTools) {
		t.Errorf("s.ToolCalls = %+v, want %+v", s.ToolCalls, wantTools)
	}
	if s.ToolErrors != 1 {
		t.Errorf("s.ToolErrors = %d, want 1", s.ToolErrors)
	}
}
