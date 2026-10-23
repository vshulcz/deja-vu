package sources

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// switchRow is one reader's session holding a command and what it printed,
// read through the registry kind. out is a word only the tool output holds;
// empty for a reader whose store keeps no output beside the call.
type switchRow struct {
	name, kind string
	fixture    func(t *testing.T, dir string) string
	cmd, out   string
	// said is speech the row holds beside the tool output, which the
	// tool-output switch must keep.
	said string
}

func parseSwitchRows() []switchRow {
	file := func(name, body string) func(*testing.T, string) string {
		return func(t *testing.T, dir string) string {
			t.Helper()
			return writeExitFixture(t, filepath.Join(dir, name), body)
		}
	}
	// Speech and a tool result in one user message: the result rode along
	// with the speech and was kept under it.
	claudeMixed := strings.Replace(failedClaudeTranscript,
		`"content":[{"type":"tool_result"`, `"content":[{"type":"text","text":"also check the logs"},{"type":"tool_result"`, 1)
	return []switchRow{
		{name: "claude", kind: "claude", fixture: file("c1.jsonl", failedClaudeTranscript), cmd: "go test ./...", out: "backoffJitter"},
		{name: "claude mixed message", kind: "claude", fixture: file("c1.jsonl", claudeMixed), cmd: "go test ./...", out: "backoffJitter", said: "also check the logs"},
		{name: "cherrystudio", kind: "cherrystudio", fixture: file("c1.jsonl", claudeMixed), cmd: "go test ./...", out: "backoffJitter", said: "also check the logs"},
		{name: "pi", kind: "pi", fixture: file("2026-09-20T10-00-00-000Z_s1.jsonl", failedPiTranscript), cmd: "go test ./...", out: "backoffJitter"},
		{name: "commandcode", kind: "commandcode", fixture: commandCodeExitStore("Exit code: 1\n./retry.go:12:5: undefined: backoffJitter"), cmd: "go test ./...", out: "backoffJitter"},
		{name: "goose jsonl", kind: "goose-jsonl", fixture: file("20260920_1.jsonl", strings.Join([]string{
			`{"role":"user","created":1790874400,"content":[{"type":"text","text":"fix the retry loop"}]}`,
			`{"role":"assistant","created":1790874401,"content":[{"type":"toolRequest","id":"c1","toolCall":{"status":"success","value":{"name":"developer__shell","arguments":{"command":"go test ./..."}}}}]}`,
			`{"role":"user","created":1790874402,"content":[{"type":"toolResponse","id":"c1","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"./retry.go:12:5: undefined: backoffJitter\n\nCommand exited with code 1"}],"isError":true}}}]}`,
		}, "\n")+"\n"), cmd: "go test ./...", out: "backoffJitter"},
		{name: "goose db", kind: "goose-db", fixture: func(t *testing.T, _ string) string {
			t.Helper()
			return seedDB(t, "sessions.db", `create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert into sessions values ('20260920_1','n','fix the retry loop','/tmp/proj','2026-09-20T10:00:00Z','2026-09-20T10:05:00Z');
insert into messages(session_id,role,content_json,created_timestamp) values ('20260920_1','user','[{"type":"text","text":"fix the retry loop"}]',1790874400);
insert into messages(session_id,role,content_json,created_timestamp) values ('20260920_1','assistant','[{"type":"toolRequest","id":"c1","toolCall":{"status":"success","value":{"name":"shell","arguments":{"command":"go test ./..."}}}}]',1790874401);
insert into messages(session_id,role,content_json,created_timestamp) values ('20260920_1','user','[{"type":"toolResponse","id":"c1","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"./retry.go:12:5: undefined: backoffJitter"}],"isError":true}}}]',1790874402);`)
		}, cmd: "go test ./...", out: "backoffJitter"},
		{name: "grok jsonl", kind: "grok", fixture: func(t *testing.T, dir string) string {
			t.Helper()
			t.Setenv("DEJA_GROK_ROOT", dir)
			const id = "01a03432-4d92-7243-ab6c-8cdd1c697738"
			return writeExitFixture(t, filepath.Join(dir, "sessions", "%2Fw%2Fapp", id, "updates.jsonl"), strings.Join([]string{
				`{"timestamp":1787582100,"method":"session/update","params":{"sessionId":"` + id + `","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"run the tests"}},"_meta":{"promptId":"p1","agentTimestampMs":1787582100000}}}`,
				`{"timestamp":1787582112,"method":"session/update","params":{"sessionId":"` + id + `","update":{"sessionUpdate":"tool_call","toolCallId":"call-1","title":"run_terminal_command","rawInput":{"command":"go test ./..."},"_meta":{"x.ai/tool":{"version":1,"name":"run_terminal_command","kind":"run_terminal_command"}}},"_meta":{"promptId":"p1","agentTimestampMs":1787582112336}}}`,
				`{"timestamp":1787582115,"method":"session/update","params":{"sessionId":"` + id + `","update":{"sessionUpdate":"tool_call_update","toolCallId":"call-1","status":"completed","content":[{"type":"content","content":{"type":"text","text":"./retry.go:12:5: undefined: backoffJitter"}}]},"_meta":{"promptId":"p1","agentTimestampMs":1787582115000}}}`,
			}, "\n")+"\n")
		}, cmd: "go test ./...", out: "backoffJitter"},
		{name: "crush", kind: "crush", fixture: func(t *testing.T, _ string) string {
			t.Helper()
			sql := "insert into sessions values ('s1',null,'retry',2,0,0,0.0,1784282405,1784282400,null,null);\n"
			sql += crushInsert(t, "m1", "s1", "assistant", 1784282400, []any{
				map[string]any{"type": "tool_call", "data": map[string]any{"id": "c1", "name": "bash", "input": `{"command":"go test ./..."}`, "finished": true}}})
			sql += crushInsert(t, "m2", "s1", "tool", 1784282401, []any{
				map[string]any{"type": "tool_result", "data": map[string]any{"tool_call_id": "c1", "name": "bash", "content": "./retry.go:12:5: undefined: backoffJitter\nExit code 1", "is_error": false}}})
			return crushStore(t, "proj", sql)
		}, cmd: "go test ./...", out: "backoffJitter"},
		{name: "copilot agent transcript", kind: "copilot-chat", fixture: func(t *testing.T, _ string) string {
			t.Helper()
			return writeCopilotAgentTranscript(t, copilotChatRoot(t), "h1", "s-agent", `{"folder":"file:///work/projA"}`, []string{
				`{"type":"session.start","data":{"sessionId":"s-agent","version":1,"producer":"copilot-agent","startTime":"2026-09-14T05:46:01.086Z"},"id":"e1","timestamp":"2026-09-14T05:46:01.086Z","parentId":null}`,
				`{"type":"user.message","data":{"content":"why does the build fail"},"id":"e2","timestamp":"2026-09-14T05:46:10.000Z","parentId":"e1"}`,
				`{"type":"tool.execution_start","data":{"toolCallId":"c2","toolName":"run_in_terminal","arguments":{"command":"go test ./..."}},"id":"e5","timestamp":"2026-09-14T05:46:13.000Z"}`,
				`{"type":"tool.execution_complete","data":{"toolCallId":"c2","success":true},"id":"e6","timestamp":"2026-09-14T05:46:14.000Z"}`,
			})
		}, cmd: "go test ./..."},
	}
}

func switchRecords(ss []model.Session) (cmds, outs []string, all string) {
	var b strings.Builder
	for _, s := range ss {
		for _, m := range s.Messages {
			switch m.Role {
			case RoleCommand:
				cmds = append(cmds, m.Text)
			case RoleToolOutput:
				outs = append(outs, m.Text)
			}
			b.WriteString(m.Text)
			b.WriteByte('\n')
		}
	}
	return cmds, outs, b.String()
}

// DEJA_INDEX_TOOL_OUTPUT=0 and DEJA_INDEX_COMMANDS=0 leave tool output and
// commands out of every reader. Six readers kept tool output under the first
// and two kept commands under the second (#5413). Each row is checked with
// both switches on first, so a row whose fixture yields neither proves
// nothing and fails.
func TestEveryReaderHonoursTheToolOutputAndCommandSwitches(t *testing.T) {
	for _, r := range parseSwitchRows() {
		t.Run(r.name, func(t *testing.T) {
			parse := func(env, val string) []model.Session {
				t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "")
				t.Setenv("DEJA_INDEX_COMMANDS", "")
				if env != "" {
					t.Setenv(env, val)
				}
				return parseKindForTest(t, r.kind, r.fixture(t, t.TempDir()))
			}
			cmds, _, all := switchRecords(parse("", ""))
			if !strings.Contains(strings.Join(cmds, "\n"), r.cmd) {
				t.Fatalf("control: commands = %q, want %q among them", cmds, r.cmd)
			}
			if r.out != "" && !strings.Contains(all, r.out) {
				t.Fatalf("control: %q is not in the session:\n%s", r.out, all)
			}

			cmds, outs, all := switchRecords(parse("DEJA_INDEX_TOOL_OUTPUT", "0"))
			if len(outs) > 0 || (r.out != "" && strings.Contains(all, r.out)) {
				t.Errorf("DEJA_INDEX_TOOL_OUTPUT=0 kept tool output:\n%s", all)
			}
			if !strings.Contains(strings.Join(cmds, "\n"), r.cmd) {
				t.Errorf("DEJA_INDEX_TOOL_OUTPUT=0 lost the command: %q", cmds)
			}
			if !strings.Contains(all, r.said) {
				t.Errorf("DEJA_INDEX_TOOL_OUTPUT=0 lost what was said, %q:\n%s", r.said, all)
			}

			cmds, _, all = switchRecords(parse("DEJA_INDEX_COMMANDS", "0"))
			if len(cmds) > 0 {
				t.Errorf("DEJA_INDEX_COMMANDS=0 kept commands %q", cmds)
			}
			if r.out != "" && !strings.Contains(all, r.out) {
				t.Errorf("DEJA_INDEX_COMMANDS=0 lost the tool output:\n%s", all)
			}
		})
	}
}

// Every parse the registry hands out, for every reader there is and every
// reader added later, goes through the one wrapper that applies the switches,
// so a reader that never tests them still has them applied.
func TestEveryRegisteredParseAppliesTheSwitches(t *testing.T) {
	for _, h := range AllHarnesses() {
		if !h.switched {
			t.Errorf("%s: Load bypasses the switches", h.Name)
		}
		for _, k := range h.Kinds {
			if !k.switched || k.Parse == nil {
				t.Errorf("%s: Parse bypasses the switches", k.Name)
			}
		}
	}
}

// Each parse switch drops its own role and nothing else.
func TestApplyParseSwitchesDropsOnlyTheSwitchedRoles(t *testing.T) {
	roles := []string{"user", "assistant", RoleToolOutput, RoleCommand, RoleFiles, RoleEdit, RoleWrote}
	session := func() []model.Session {
		msgs := make([]model.Message, 0, len(roles))
		for _, r := range roles {
			msgs = append(msgs, model.Message{Role: r, Text: r})
		}
		return []model.Session{{ID: "s", Messages: msgs}}
	}
	for env, role := range map[string]string{
		"DEJA_INDEX_TOOL_OUTPUT": RoleToolOutput,
		"DEJA_INDEX_COMMANDS":    RoleCommand,
		"DEJA_INDEX_PATHS":       RoleFiles,
		"DEJA_INDEX_EDITS":       RoleEdit,
		"DEJA_INDEX_WRITES":      RoleWrote,
	} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, "0")
			parsed := ApplyParseSwitches(session())[0].Messages
			got := make([]string, 0, len(parsed))
			for _, m := range parsed {
				got = append(got, m.Role)
			}
			var want []string
			for _, r := range roles {
				if r != role {
					want = append(want, r)
				}
			}
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("roles = %v, want %v", got, want)
			}
		})
	}
	if got := len(ApplyParseSwitches(session())[0].Messages); got != len(roles) {
		t.Errorf("every switch on: %d records, want %d", got, len(roles))
	}
	t.Setenv("DEJA_INDEX_COMMANDS", "0")
	for _, m := range ParseSwitched(session()[0]).Messages {
		if m.Role == RoleCommand {
			t.Error("a session read outside the registry kept its commands")
		}
	}
}

// A sub-agent run cut to its last turns is cut after the switches, so the
// turns it keeps are ones the index keeps: cut first, a child whose tail was
// commands and their output kept less under =0 than one whose reader dropped
// them itself.
func TestSubagentTailIsCutAfterTheParseSwitches(t *testing.T) {
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	msgs := make([]model.Message, 0, 12)
	for range 6 {
		msgs = append(msgs, model.Message{Role: "assistant", Text: "turn"}, model.Message{Role: RoleToolOutput, Text: "out"})
	}
	ss := applyReadSwitches([]model.Session{{ID: "kid", Kind: "subagent", Messages: append([]model.Message{{Role: "user", Text: "the task"}}, msgs...)}})
	want := applyReadSwitches([]model.Session{{ID: "kid", Kind: "subagent", Messages: append([]model.Message{{Role: "user", Text: "the task"}}, dropRoles(msgs, map[string]bool{RoleToolOutput: true})...)}})
	if len(ss[0].Messages) != len(want[0].Messages) {
		t.Errorf("kept %d records, want %d as from a reader that drops the output itself", len(ss[0].Messages), len(want[0].Messages))
	}
}

// opencode sessions whose diffs changed are read beside the registry, and
// get the same switches its kinds apply.
func TestOpencodeDiffSessionsGetTheSwitches(t *testing.T) {
	db := seedDB(t, "opencode.db", `create table session(id text, parent_id text, directory text, title text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('ses_parent',NULL,'/w','fix it','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into session values('ses_child','ses_parent','/w','review (@general subagent)','2026-01-02T03:10:00Z','2026-01-02T03:20:00Z');
insert into message values('m1','ses_parent',1767409200000,'{"role":"user","agent":"build"}');
insert into part values('p1','m1','{"type":"text","text":"fix the retry loop","time":{"start":"2026-01-02T03:00:00Z"}}');
insert into message values('m2','ses_child',1767409800000,'{"role":"user","agent":"general"}');
insert into part values('p2','m2','{"type":"text","text":"review the retry loop","time":{"start":"2026-01-02T03:10:00Z"}}');`)
	t.Setenv("DEJA_OPENCODE_DB", db)
	diffs := t.TempDir()
	t.Setenv("DEJA_OPENCODE_DIFFS", diffs)
	paths := make([]string, 0, 2)
	for _, id := range []string{"ses_parent", "ses_child"} {
		p := filepath.Join(diffs, id+".json")
		writeExitFixture(t, p, opencodeDiffFixture)
		paths = append(paths, p)
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	t.Setenv("DEJA_INDEX_EDITS", "0")
	ss, err := ParseOpencodeDiffSessions(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "ses_parent" {
		t.Fatalf("sessions = %d, want the parent alone under DEJA_INCLUDE_SUBAGENTS=0", len(ss))
	}
	for _, m := range ss[0].Messages {
		if m.Role == RoleEdit {
			t.Errorf("DEJA_INDEX_EDITS=0 kept an edit: %.60q", m.Text)
		}
	}
}
