package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

var failedClaudeTranscript = strings.Join([]string{
	`{"type":"user","sessionId":"c1","timestamp":"2026-10-01T10:00:00Z","cwd":"/tmp/proj","message":{"role":"user","content":"fix the retry loop"}}`,
	`{"type":"assistant","sessionId":"c1","timestamp":"2026-10-01T10:00:01Z","cwd":"/tmp/proj","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}`,
	`{"type":"user","sessionId":"c1","timestamp":"2026-10-01T10:00:02Z","cwd":"/tmp/proj","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"Exit code 1\n./retry.go:12:5: undefined: backoffJitter\nFAIL"}]}}`,
}, "\n") + "\n"

var failedPiTranscript = strings.Join([]string{
	`{"type":"session","version":3,"id":"pi-s1","timestamp":"2026-09-20T10:00:00.000Z","cwd":"/tmp/proj"}`,
	`{"type":"message","id":"u1","timestamp":"2026-09-20T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]}}`,
	`{"type":"message","id":"a1","timestamp":"2026-09-20T10:00:01.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"go test ./..."}}]}}`,
	`{"type":"message","id":"r1","timestamp":"2026-09-20T10:00:02.000Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"bash","content":[{"type":"text","text":"./retry.go:12:5: undefined: backoffJitter\n\nCommand exited with code 1"}],"details":{},"isError":true}}`,
}, "\n") + "\n"

// A command that failed carries its exit code in every harness that writes it
// down, each in its own place: a status line before or after the output, a
// field beside it, or a result filed apart from the call. Each row is one call
// to `go test ./...` and the failing result that harness writes for it
// (#4487, #4501, #4496, #4502, #4505, #4507, #4493).
func TestAFailedCommandKeepsItsExitCode(t *testing.T) {
	const want = "$ go test ./...  → exit 1"
	write := func(t *testing.T, p, body string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	file := func(name, body string) func(*testing.T, string) string {
		return func(t *testing.T, dir string) string { return write(t, filepath.Join(dir, name), body) }
	}

	rows := []struct {
		name, kind string
		fixture    func(*testing.T, string) string
		parse      func(string) ([]model.Session, error)
	}{
		{name: "claude", kind: "claude", fixture: file("c1.jsonl", failedClaudeTranscript)},
		{name: "claude reference parser", fixture: file("c1.jsonl", failedClaudeTranscript), parse: func(p string) ([]model.Session, error) { return parseClaudeGenericFromOffset(p, 0) }},
		{name: "cherrystudio", kind: "cherrystudio", fixture: file("c1.jsonl", failedClaudeTranscript)},
		{name: "pi", kind: "pi", fixture: file("2026-09-20T10-00-00-000Z_s1.jsonl", failedPiTranscript)},
		{name: "omp", kind: "omp", fixture: file("2026-09-20T10-00-00-000Z_s1.jsonl", failedPiTranscript)},
		{name: "gjc", kind: "gjc", fixture: file("2026-09-20T10-00-00-000Z_s1.jsonl", failedPiTranscript)},
		{name: "prime", kind: "prime", fixture: file("2026-09-20T10-00-00-000Z_s1.jsonl", failedPiTranscript)},
		{name: "senpi", kind: "senpi", fixture: file("2026-09-20T10-00-00-000Z_s1.jsonl", failedPiTranscript)},
		{name: "kimchi", kind: "kimchi", fixture: file("2026-09-20T10-00-00-000Z_s1.jsonl", failedPiTranscript)},
		{name: "cherrystudio-pi", fixture: file("2026-09-20T10-00-00-000Z_s1.jsonl", failedPiTranscript), parse: func(p string) ([]model.Session, error) { return ParseCherryStudioPiFileFromOffset(p, 0) }},
		{name: "goose", kind: "goose-db", fixture: func(t *testing.T, _ string) string {
			return seedDB(t, "sessions.db", `create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert into sessions values ('20260920_1','n','fix the retry loop','/tmp/proj','2026-09-20T10:00:00Z','2026-09-20T10:05:00Z');
insert into messages(session_id,role,content_json,created_timestamp) values ('20260920_1','user','[{"type":"text","text":"fix the retry loop"}]',1790874400);
insert into messages(session_id,role,content_json,created_timestamp) values ('20260920_1','assistant','[{"type":"toolRequest","id":"c1","toolCall":{"status":"success","value":{"name":"shell","arguments":{"command":"go test ./..."}}}}]',1790874401);
insert into messages(session_id,role,content_json,created_timestamp) values ('20260920_1','user','[{"type":"toolResponse","id":"c1","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"./retry.go:12:5: undefined: backoffJitter\n\nCommand exited with code 1"}],"isError":true,"structuredContent":{"stdout":"","stderr":"./retry.go:12:5: undefined: backoffJitter","exit_code":1,"timed_out":false}}}}]',1790874402);`)
		}},
		{name: "cline cli", kind: "cline-sdk", fixture: func(t *testing.T, dir string) string {
			id := "1790877871094"
			write(t, filepath.Join(dir, id, id+".json"), `{"cwd":"/tmp/proj","started_at":"2026-10-01T18:00:00Z"}`)
			return write(t, filepath.Join(dir, id, id+".messages.json"), `{"version":1,"agent":"lead","sessionId":"`+id+`","messages":[
 {"role":"user","ts":1790877871100,"content":[{"type":"text","text":"fix the retry loop"}]},
 {"role":"assistant","ts":1790877871200,"content":[{"type":"tool_use","id":"call_1","name":"run_commands","input":{"commands":["go vet ./...","go test ./..."]}}]},
 {"role":"user","ts":1790877871300,"content":[{"type":"tool_result","tool_use_id":"call_1","name":"run_commands","content":[
  {"query":"go vet ./...","result":"","success":true},
  {"query":"go test ./...","result":"[Command exited with code 1]\n\n[stderr]\n./retry.go:12:5: undefined: backoffJitter\n","error":"Command exited with code 1","success":false}]}]}]}`)
		}},
		{name: "cline vscode", kind: "cline-vscode", fixture: func(t *testing.T, dir string) string {
			write(t, filepath.Join(dir, "state", "taskHistory.json"), `[{"id":"1790000000000","ts":1790000000000,"task":"fix the retry loop","cwdOnTaskInitialization":"/tmp/proj"}]`)
			return write(t, filepath.Join(dir, "tasks", "1790000000000", "api_conversation_history.json"), `[
 {"role":"user","content":[{"type":"text","text":"<task>fix the retry loop</task>"}]},
 {"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"execute_command","input":{"command":"go test ./...","requires_approval":false}}]},
 {"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"Command failed with exit code 1.\nOutput:\n./retry.go:12:5: undefined: backoffJitter"}]}]`)
		}},
		{name: "kiro-cli", kind: "kiro-cli", fixture: func(t *testing.T, dir string) string {
			return write(t, filepath.Join(dir, "cli", "11111111-2222-4333-8444-555555555555.jsonl"), strings.Join([]string{
				`{"version":"v1","kind":"Prompt","data":{"message_id":"m0","content":[{"kind":"text","data":"fix the retry loop"}],"meta":{"timestamp":1790848800}}}`,
				`{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m1","content":[{"kind":"toolUse","data":{"toolUseId":"t1","name":"shell","input":{"command":"go test ./..."}}}],"meta":{"timestamp":1790848801}}}`,
				`{"version":"v1","kind":"ToolResults","data":{"message_id":"m2","content":[{"kind":"toolResult","data":{"toolUseId":"t1","content":[{"kind":"json","data":{"exit_status":"exit status: 1","stdout":"","stderr":"./retry.go:12:5: undefined: backoffJitter"}}],"status":"success"}}],"meta":{"timestamp":1790848802}}}`,
			}, "\n")+"\n")
		}},
		{name: "kiro-cli no-interactive", kind: "kiro-db", fixture: func(t *testing.T, _ string) string {
			return kiroTestDB(t, kiroDBRow("/tmp/proj", "bf73e1f5-0000-4000-8000-000000000003", `{"conversation_id":"bf73e1f5-0000-4000-8000-000000000003","history":[
 {"user":{"content":{"Prompt":{"prompt":"fix the retry loop"}},"timestamp":"2026-10-01T10:00:00+00:00"},
  "assistant":{"ToolUse":{"message_id":"m1","content":"Running the tests.","tool_uses":[{"id":"t1","name":"execute_bash","args":{"command":"go test ./..."}}]}}},
 {"user":{"content":{"ToolUseResults":{"tool_use_results":[{"tool_use_id":"t1","content":[{"Json":{"exit_status":"1","stdout":"","stderr":"./retry.go:12:5: undefined: backoffJitter"}}],"status":"Success"}]}},"timestamp":null},
  "assistant":{"Response":{"message_id":"m2","content":"The build is broken."}}}]}`, 1790848800000))
		}},
		{name: "copilot-chat", kind: "copilot-chat", fixture: func(t *testing.T, dir string) string {
			return write(t, filepath.Join(dir, "workspaceStorage", "a1b2", "chatSessions", "5c0ffee0-0000-4000-8000-000000000000.jsonl"), strings.Join([]string{
				`{"kind":0,"v":{"version":3,"sessionId":"5c0ffee0-0000-4000-8000-000000000000","creationDate":1790000000000,"customTitle":"fix the retry loop","requests":[]}}`,
				`{"kind":2,"k":["requests"],"v":[{"requestId":"request_1","timestamp":1790000001000,"message":{"text":"fix the retry loop"},"response":[{"kind":"toolInvocationSerialized","isComplete":true,"toolCallId":"c2","toolId":"run_in_terminal","toolSpecificData":{"kind":"terminal","commandLine":{"original":"go test ./..."},"language":"sh","terminalCommandState":{"exitCode":1,"timestamp":1790000003000,"duration":1200},"terminalCommandOutput":{"text":"./retry.go:12:5: undefined: backoffJitter\nFAIL"}}}]}]}`,
			}, "\n")+"\n")
		}},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			p := r.fixture(t, t.TempDir())
			var ss []model.Session
			if r.parse != nil {
				var err error
				if ss, err = r.parse(p); err != nil {
					t.Fatal(err)
				}
			} else {
				ss = parseKindForTest(t, r.kind, p)
			}
			assertCommand(t, ss, want)
		})
	}

	// Zed keeps the call and its result on one agent message.
	t.Run("zed", func(t *testing.T) {
		msg, _ := json.Marshal(map[string]any{"Agent": map[string]any{
			"content": []any{map[string]any{"ToolUse": map[string]any{"id": "c1", "name": "terminal", "input": map[string]any{"command": "go test ./...", "cd": "/tmp/proj"}}}},
			"tool_results": map[string]any{"c1": map[string]any{"tool_use_id": "c1", "tool_name": "terminal", "is_error": false,
				"content": map[string]any{"Text": "Command \"go test ./...\" failed with exit code 1.\n\n```\n./retry.go:12:5: undefined: backoffJitter\n```"}}},
		}})
		assertCommand(t, []model.Session{{Messages: zedWork(msg, time.Unix(1790000000, 0))}}, want)
	})
}

func assertCommand(t *testing.T, ss []model.Session, want string) {
	t.Helper()
	var got []string
	for _, s := range ss {
		for _, m := range s.Messages {
			if m.Role == RoleCommand {
				if m.Text == want {
					return
				}
				got = append(got, m.Text)
			}
		}
	}
	t.Errorf("commands = %q, want %q among them", got, want)
}

// A kiro-cli result that lands a pass after its call is read whole when it
// failed, or the stored call never gets its exit (#4505); a clean one is not.
func TestKiroCLIReadsAFailedResultWithItsCall(t *testing.T) {
	call := `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m1","content":[{"kind":"toolUse","data":{"toolUseId":"t1","name":"shell","input":{"command":"go test ./..."}}}],"meta":{"timestamp":1790848801}}}` + "\n"
	result := func(status string) string {
		return `{"version":"v1","kind":"ToolResults","data":{"message_id":"m2","content":[{"kind":"toolResult","data":{"toolUseId":"t1","content":[{"kind":"json","data":{"exit_status":"` + status + `","stdout":"","stderr":""}}],"status":"success"}}],"meta":{"timestamp":1790848802}}}` + "\n"
	}
	for status, want := range map[string]bool{"exit status: 1": false, "exit status: 0": true} {
		p := filepath.Join(t.TempDir(), "s.jsonl")
		if err := os.WriteFile(p, []byte(call+result(status)), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := KiroCLIResumes(p, int64(len(call))); got != want {
			t.Errorf("%s: resumes = %v, want %v", status, got, want)
		}
	}
}

// A Cline CLI batch can run the same command twice. Each entry's status
// belongs to one of the two, in order: the first clean entry stamped both and
// the failure that followed was dropped as already stamped.
func TestClineBatchStampsARepeatedCommandOncePerEntry(t *testing.T) {
	dir := t.TempDir()
	id := "1790877871094"
	if err := os.MkdirAll(filepath.Join(dir, id), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id, id+".json"), []byte(`{"cwd":"/tmp/proj","started_at":"2026-10-01T18:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, id, id+".messages.json")
	if err := os.WriteFile(p, []byte(`{"version":1,"agent":"lead","sessionId":"`+id+`","messages":[
 {"role":"user","ts":1790877871100,"content":[{"type":"text","text":"fix the retry loop"}]},
 {"role":"assistant","ts":1790877871200,"content":[{"type":"tool_use","id":"call_1","name":"run_commands","input":{"commands":["go test ./...","go test ./..."]}}]},
 {"role":"user","ts":1790877871300,"content":[{"type":"tool_result","tool_use_id":"call_1","name":"run_commands","content":[
  {"query":"go test ./...","result":"ok","success":true},
  {"query":"go test ./...","result":"FAIL","error":"Command exited with code 1","success":false}]}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range parseKindForTest(t, "cline-sdk", p) {
		for _, m := range s.Messages {
			if m.Role == RoleCommand {
				got = append(got, m.Text)
			}
		}
	}
	want := []string{"$ go test ./...  → exit 0", "$ go test ./...  → exit 1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("commands = %q, want %q", got, want)
	}
}

// A failed result that lands a pass after its call sends the file back for a
// whole read in every appendable format whose reader now stamps the failure,
// as kiro-cli's does above; a clean one, or a call and failure both in the
// tail, does not (#4443).
func TestAFailedResultForAStoredCallIsReadWithItsCall(t *testing.T) {
	claudeCall := `{"type":"assistant","sessionId":"c1","timestamp":"2026-10-01T10:00:01Z","cwd":"/tmp/proj","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}` + "\n"
	claudeOut := func(failed bool) string {
		if failed {
			return `{"type":"user","sessionId":"c1","timestamp":"2026-10-01T10:00:02Z","cwd":"/tmp/proj","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"Exit code 1\nFAIL"}]}}` + "\n"
		}
		return `{"type":"user","sessionId":"c1","timestamp":"2026-10-01T10:00:02Z","cwd":"/tmp/proj","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"ok"}]}}` + "\n"
	}
	gooseCall := `{"role":"assistant","created":1790874401,"content":[{"type":"toolRequest","id":"c1","toolCall":{"status":"success","value":{"name":"developer__shell","arguments":{"command":"go test ./..."}}}}]}` + "\n"
	gooseOut := func(failed bool) string {
		if failed {
			return `{"role":"user","created":1790874402,"content":[{"type":"toolResponse","id":"c1","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"FAIL\n\nCommand exited with code 1"}],"isError":true}}}]}` + "\n"
		}
		return `{"role":"user","created":1790874402,"content":[{"type":"toolResponse","id":"c1","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"ok"}],"structuredContent":{"exit_code":0}}}}]}` + "\n"
	}
	resumes := func(kind string) func(string, int64) bool {
		for _, h := range Registry() {
			for _, k := range h.Kinds {
				if k.Name == kind {
					return k.Resumes
				}
			}
		}
		t.Fatalf("no kind %s", kind)
		return nil
	}
	for _, c := range []struct {
		kind, head, tail string
		want             bool
	}{
		{"claude", claudeCall, claudeOut(true), false},
		{"claude", claudeCall, claudeOut(false), true},
		{"claude", "", claudeCall + claudeOut(true), true},
		{"cherrystudio", claudeCall, claudeOut(true), false},
		{"goose-jsonl", gooseCall, gooseOut(true), false},
		{"goose-jsonl", gooseCall, gooseOut(false), true},
		{"goose-jsonl", gooseCall, `{"role":"user","created":1790874402,"content":[{"type":"toolResponse","id":"c1","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"FAIL"}],"structuredContent":{"exit_code":2}}}}]}` + "\n", false},
		{"goose-jsonl", "", gooseCall + gooseOut(true), true},
	} {
		p := filepath.Join(t.TempDir(), "s.jsonl")
		body := `{"type":"user","sessionId":"c1","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"fix the retry loop"}}` + "\n" + c.head
		if err := os.WriteFile(p, []byte(body+c.tail), 0o644); err != nil {
			t.Fatal(err)
		}
		// A kind with no Resumes appends every tail.
		got := true
		if r := resumes(c.kind); r != nil {
			got = r(p, int64(len(body)))
		}
		if got != c.want {
			t.Errorf("%s head=%t tail %q: resumes = %v, want %v", c.kind, c.head != "", c.tail, got, c.want)
		}
	}
}
