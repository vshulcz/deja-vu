package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// exitRow is one client's store holding a single `go test ./...` and the
// result that client writes for it, read through the registry kind.
type exitRow struct {
	name, kind string
	fixture    func(t *testing.T, dir string) string
	want       string
}

func writeExitFixture(t *testing.T, p, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func commandsOf(ss []model.Session) []string {
	var out []string
	for _, s := range ss {
		for _, m := range s.Messages {
			if m.Role == RoleCommand {
				out = append(out, m.Text)
			}
		}
	}
	return out
}

func runExitRows(t *testing.T, rows []exitRow) {
	t.Helper()
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			got := commandsOf(parseKindForTest(t, r.kind, r.fixture(t, t.TempDir())))
			if len(got) != 1 || got[0] != r.want {
				t.Errorf("commands = %q, want [%q]", got, r.want)
			}
		})
	}
}

// rooTask is a Roo (or Kilo Code) task directory whose one execute_command
// came back with result.
func rooExitTask(result string) func(*testing.T, string) string {
	return func(t *testing.T, dir string) string {
		task := filepath.Join(dir, "tasks", "1788845325718")
		writeExitFixture(t, filepath.Join(task, "history_item.json"), `{"id":"1788845325718","ts":1788845325718,"task":"fix the retry loop","workspace":"/tmp/proj"}`)
		return writeExitFixture(t, filepath.Join(task, "api_conversation_history.json"), `[
 {"role":"user","content":[{"type":"text","text":"<task>\nfix the retry loop\n</task>"}]},
 {"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"execute_command","input":{"command":"go test ./...","cwd":"/tmp/proj"}}]},
 {"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":`+vocabJSON(result)+`}]}]`)
	}
}

func continueExitStore(state string) func(*testing.T, string) string {
	return func(t *testing.T, dir string) string {
		return writeExitFixture(t, filepath.Join(dir, "sessions", "s1.json"), `{"sessionId":"s1","title":"fix the retry loop","workspaceDirectory":"/tmp/proj","history":[`+
			`{"message":{"role":"user","content":"fix the retry loop"},"contextItems":[]},`+
			`{"message":{"role":"assistant","content":"","toolCalls":[]},"contextItems":[],"toolCallStates":[`+state+`]}]}`)
	}
}

func ampExitThread(run string) func(*testing.T, string) string {
	return func(t *testing.T, dir string) string {
		return writeExitFixture(t, filepath.Join(dir, "T-0f3c.json"), `{"v":7,"id":"T-0f3c","created":1774950000000,"title":"Fix the retry loop","env":{"initial":{"trees":[{"uri":"file:///tmp/proj"}]}},"messages":[`+
			`{"role":"user","content":[{"type":"text","text":"fix the retry loop"}],"meta":{"sentAt":1774950001000}},`+
			`{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01","name":"Bash","complete":true,"input":{"cmd":"go test ./...","cwd":"/tmp/proj"}}],"usage":{"timestamp":"2026-03-31T09:40:05Z"}},`+
			`{"role":"user","content":[{"type":"tool_result","toolUseID":"toolu_01","run":`+run+`}]}]}`)
	}
}

func antigravityExitTranscript(steps ...string) func(*testing.T, string) string {
	return func(t *testing.T, dir string) string {
		lines := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-09-30T10:00:00Z","content":"<USER_REQUEST>\nfix the retry loop\n</USER_REQUEST>"}` + "\n" +
			`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-30T10:00:05Z","content":"","tool_calls":[{"name":"run_command","args":{"CommandLine":"\"go test ./...\"","Cwd":"\"/tmp/proj\""}}]}` + "\n"
		for _, s := range steps {
			lines += `{"step_index":2,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-09-30T10:00:09Z","content":` + vocabJSON(s) + `}` + "\n"
		}
		return writeExitFixture(t, filepath.Join(dir, "brain", "b0c1d2e3", ".system_generated", "logs", "transcript.jsonl"), lines)
	}
}

// Roo, Kilo Code, Continue, Amp and Antigravity each write how a command
// ended, in their own words, and the command record never carried it (#4530).
// A status is read only where the client frames it; output that merely says
// "exit code" leaves the command as it was.
func TestRooContinueAmpAntigravityKeepTheExitCode(t *testing.T) {
	const roo = "Command executed in terminal within working directory '/tmp/proj'. "
	cnBash := func(status, out string) string {
		return `{"toolCall":{"id":"c1","type":"function","function":{"name":"Bash","arguments":"{\"command\":\"go test ./...\"}"}},"status":"` + status + `","parsedArgs":{"command":"go test ./..."},"output":[{"name":"Tool Result","description":"Tool output","content":` + vocabJSON(out) + `}]}`
	}
	ideRun := func(status string) string {
		return `{"toolCall":{"id":"c1","type":"function","function":{"name":"run_terminal_command","arguments":"{\"command\":\"go test ./...\"}"}},"status":"done","parsedArgs":{"command":"go test ./...","waitForCompletion":true},"output":[{"name":"Terminal","description":"Terminal command output","content":"./retry.go:12:5: undefined: backoffJitter","status":` + vocabJSON(status) + `}]}`
	}
	runExitRows(t, []exitRow{
		{"roo failed", "roo", rooExitTask(roo + "Command execution was not successful, inspect the cause and adjust as needed.\nExit code: 1\nOutput:\n./retry.go:12:5: undefined: backoffJitter"), "$ go test ./...  → exit 1"},
		{"roo clean", "roo", rooExitTask(roo + "Exit code: 0\nOutput:\nok  \tproj\t0.01s"), "$ go test ./...  → exit 0"},
		{"roo persisted output", "roo", rooExitTask("Command executed in '/tmp/proj'. Command execution was not successful, inspect the cause and adjust as needed.\nExit code: 2\n\nOutput (2.1MB) persisted. Artifact ID: a1"), "$ go test ./...  → exit 2"},
		{"roo signal", "roo", rooExitTask(roo + "Process terminated by signal SIGKILL\nOutput:\n"), "$ go test ./..."},
		{"roo output naming a code", "roo", rooExitTask("Exit code: 1\nOutput:\nfine"), "$ go test ./..."},
		{"kilo code task", "kilocode-task", rooExitTask(roo + "Command execution was not successful, inspect the cause and adjust as needed.\nExit code: 1\nOutput:\nFAIL"), "$ go test ./...  → exit 1"},
		{"continue cn failed", "continue", continueExitStore(cnBash("errored", "Error executing tool Bash: Error (exit code 1): ./retry.go:12:5: undefined: backoffJitter")), "$ go test ./...  → exit 1"},
		// A cn Bash that exits non-zero with nothing on stderr resolves as
		// done, so done says nothing about the code.
		{"continue cn done", "continue", continueExitStore(cnBash("done", "ok  \tproj\t0.01s")), "$ go test ./..."},
		{"continue cn other error", "continue", continueExitStore(cnBash("errored", "Error executing tool Bash: spawn sh ENOENT")), "$ go test ./..."},
		{"continue ide failed", "continue", continueExitStore(ideRun("Command failed with exit code 1")), "$ go test ./...  → exit 1"},
		{"continue ide failed, exec path", "continue", continueExitStore(ideRun("Command failed with: Command failed with exit code 2")), "$ go test ./...  → exit 2"},
		{"continue ide completed", "continue", continueExitStore(ideRun("Command completed")), "$ go test ./..."},
		{"amp failed", "amp", ampExitThread(`{"status":"done","result":{"output":"./retry.go:12:5: undefined: backoffJitter","exitCode":1}}`), "$ go test ./...  → exit 1"},
		{"amp clean", "amp", ampExitThread(`{"status":"done","result":{"output":"ok","exitCode":0}}`), "$ go test ./...  → exit 0"},
		{"amp unknown", "amp", ampExitThread(`{"status":"done","result":{"output":"","exitCode":-1}}`), "$ go test ./..."},
		{"amp cancelled", "amp", ampExitThread(`{"status":"cancelled"}`), "$ go test ./..."},
		{"antigravity failed", "antigravity", antigravityExitTranscript("Created At: 2026-09-30T10:00:05Z\nCompleted At: 2026-09-30T10:00:09Z\n\nThe command exited with code 1.\nOutput:\n./retry.go:12:5: undefined: backoffJitter"), "$ go test ./...  → exit 1"},
		{"antigravity clean", "antigravity", antigravityExitTranscript("Created At: 2026-09-30T10:00:05Z\n\nThe command exited with code 0.\nOutput:\nok"), "$ go test ./...  → exit 0"},
		{"antigravity planner speaking first", "antigravity", func(t *testing.T, dir string) string {
			p := antigravityExitTranscript("The command exited with code 1.\nOutput:\nFAIL")(t, dir)
			b, _ := os.ReadFile(p)
			return writeExitFixture(t, p, strings.Replace(string(b), `"content":"","tool_calls"`, `"content":"Running the tests.","tool_calls"`, 1))
		}, "$ go test ./...  → exit 1"},
		{"antigravity output naming a code", "antigravity", antigravityExitTranscript("Created At: 2026-09-30T10:00:05Z\nOutput:\nThe command exited with code 1."), "$ go test ./..."},
	})
}

// crush.db keeps a call and its result as separate parts on separate rows, and
// the reader recorded each on its own: a failed bash lost the "Exit code N"
// its result ends with, and an edit crush refused left edit and wrote records
// of text the file never held (#4532).
func TestCrushJoinsACallToItsResult(t *testing.T) {
	const oldLoop = "\tfor {"
	edit := `{"file_path":"/tmp/proj/retry.go","old_string":"\tfor {","new_string":"\tfor attempt := 0; attempt < maxAttempts; attempt++ {"}`
	cases := []struct {
		id, tool, input, result string
		isErr                   bool
		want                    []string
	}{
		{"bash_fail", "bash", `{"command":"go vet ./retry"}`, "./retry.go:12:5: undefined: backoffJitter\nExit code 1\n\n<cwd>/tmp/proj</cwd>", false, []string{"command go vet ./retry  → exit 1"}},
		{"bash_ok", "bash", `{"command":"go test ./retry"}`, "ok  retry 0.01s\n\n<cwd>/tmp/proj</cwd>", false, []string{"command go test ./retry"}},
		// The code is crush's last line, not one quoted in the output.
		{"bash_quoting", "bash", `{"command":"go test ./retry"}`, "Exit code 1\nok  retry 0.01s\n\n<cwd>/tmp/proj</cwd>", false, []string{"command go test ./retry"}},
		{"edit_ok", "edit", edit, "Content replaced in file", false, []string{"files /tmp/proj/retry.go", "edit /tmp/proj/retry.go\n" + oldLoop, "wrote"}},
		{"edit_refused", "edit", edit, "you must read the file before editing it. Use the View tool first", true, []string{"files /tmp/proj/retry.go"}},
		{"edit_unanswered", "edit", edit, "", false, []string{"files /tmp/proj/retry.go", "edit /tmp/proj/retry.go\n" + oldLoop, "wrote"}},
	}
	sql := ""
	for i, c := range cases {
		sql += "insert into sessions values ('" + c.id + "',null,'retry',2,0,0,0.0," + itoa(1784282405+i*10) + "," + itoa(1784282400+i*10) + ",null,null);\n"
		sql += crushInsert(t, "a_"+c.id, c.id, "assistant", int64(1784282400+i*10), []any{
			map[string]any{"type": "tool_call", "data": map[string]any{"id": "c_" + c.id, "name": c.tool, "input": c.input, "finished": true}}})
		if c.result != "" {
			sql += crushInsert(t, "t_"+c.id, c.id, "tool", int64(1784282401+i*10), []any{
				map[string]any{"type": "tool_result", "data": map[string]any{"tool_call_id": "c_" + c.id, "name": c.tool, "content": c.result, "is_error": c.isErr}}})
		}
	}
	ss, err := ParseCrushDB(crushStore(t, "proj", sql))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		var got []string
		for _, s := range ss {
			if s.ID != c.id {
				continue
			}
			for _, m := range s.Messages {
				switch m.Role {
				case RoleToolOutput:
				case RoleWrote:
					got = append(got, "wrote")
				default:
					got = append(got, m.Role+" "+m.Text)
				}
			}
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: records = %q, want %q", c.id, got, c.want)
		}
	}
}

// ZCode writes a failed Bash Claude's way, "Exit code N" as the first line of
// the output, and leaves metadata.exit unset, which is all the reader looked
// at (#4536). opencode's own parts, with metadata.exit, are read as before.
func TestZCodeFailedBashKeepsItsExitCode(t *testing.T) {
	part := func(id, output, meta string) string {
		return "insert into part values ('" + id + "','m_" + id + "','" + strings.ReplaceAll(`{"type":"tool","tool":"Bash","callID":"`+id+`","state":{"status":"completed","input":{"command":"go vet ./retry"},"output":`+vocabJSON(output)+meta+`,"time":{"start":1790000002000}}}`, "'", "''") + "');\n" +
			"insert into message values ('m_" + id + "','" + id + "',1790000001000,'{\"role\":\"assistant\",\"time\":{\"created\":1790000001000}}');\n" +
			"insert into session values ('" + id + "','/tmp/proj',1790000000000,1790000100000);\n"
	}
	db := vocabSQL(t, `create table session(id text primary key, directory text, time_created integer, time_updated integer);
create table message(id text, session_id text, time_created integer, data text);
create table part(id text, message_id text, data text);
`+part("failed", "Exit code 1\n./retry.go:12:5: undefined: backoffJitter", "")+
		part("clean", "ok  retry 0.01s", "")+
		part("quoting", "ok\nExit code 1", "")+
		part("meta", "Exit code 1\n./retry.go:12:5: undefined: backoffJitter", `,"metadata":{"exit":2}`))
	want := map[string][2]string{
		"failed":  {"$ go vet ./retry  → exit 1", "./retry.go:12:5: undefined: backoffJitter"},
		"clean":   {"$ go vet ./retry", "ok  retry 0.01s"},
		"quoting": {"$ go vet ./retry", "ok\nExit code 1"},
		"meta":    {"$ go vet ./retry  → exit 2", "Exit code 1\n./retry.go:12:5: undefined: backoffJitter"},
	}
	ss := parseKindForTest(t, "zcode-db", db)
	if len(ss) != len(want) {
		t.Fatalf("%d sessions, want %d", len(ss), len(want))
	}
	for _, s := range ss {
		got := [2]string{strings.Join(rolesOf(s, RoleCommand), "|"), strings.Join(rolesOf(s, RoleToolOutput), "|")}
		if got != want[s.ID] {
			t.Errorf("%s: command, output = %q, want %q", s.ID, got, want[s.ID])
		}
	}
}

// codeWhaleSaved is a saved CodeWhale session whose one bash call came back
// with result.
func codeWhaleSaved(result string) func(*testing.T, string) string {
	return func(t *testing.T, dir string) string {
		return writeExitFixture(t, filepath.Join(dir, "cw-1.json"), `{"schema_version":3,
"metadata":{"id":"cw-1","title":"fix the retry loop","created_at":"2026-10-01T10:00:00Z","updated_at":"2026-10-01T10:05:00Z","workspace":"/tmp/proj"},
"messages":[
 {"role":"user","content":[{"type":"text","text":"fix the retry loop"}]},
 {"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"bash","input":{"command":"go test ./..."}}]},
 {"role":"user","content":[{"type":"tool_result","tool_use_id":"c1",`+result+`}]}]}`)
	}
}

// CodeWhale ends a failed bash result with "Command exited with code N" and
// marks it is_error (tools/shell.rs contract_bash_error_status); the reader
// never joined a result to its call (#4537).
func TestCodeWhaleFailedBashKeepsItsExitCode(t *testing.T) {
	runExitRows(t, []exitRow{
		{"failed", "codewhale", codeWhaleSaved(`"is_error":true,"content":"Error: --- FAIL: TestRetry (0.00s)\n    retry_test.go:12: retried 0 times\nFAIL\n\nCommand exited with code 1"`), "$ go test ./...  → exit 1"},
		{"clean", "codewhale", codeWhaleSaved(`"content":"ok  \tproj\t0.01s"`), "$ go test ./..."},
		{"timed out", "codewhale", codeWhaleSaved(`"is_error":true,"content":"Error: Command timed out after 120 seconds"`), "$ go test ./..."},
		{"not an error", "codewhale", codeWhaleSaved(`"content":"printed\nCommand exited with code 1"`), "$ go test ./..."},
	})
}

const commandCodeCall = `{"type":"message","id":"a1","timestamp":"2026-10-01T20:09:15.324Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"shell_command","input":{"command":"go test ./..."}}],"meta":{"source":"model"}}}` + "\n"

func commandCodeResult(text string) string {
	return `{"type":"message","id":"r1","timestamp":"2026-10-01T20:09:16.324Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"text","text":` + vocabJSON(text) + `}]}],"meta":{"source":"tool"}}}` + "\n"
}

func commandCodeExitStore(result string) func(*testing.T, string) string {
	return func(t *testing.T, dir string) string {
		return writeExitFixture(t, filepath.Join(dir, "-tmp-proj", "s1.jsonl"),
			`{"type":"session","version":3,"id":"s1","timestamp":"2026-10-01T20:09:14.839Z","cwd":"/tmp/proj"}`+"\n"+commandCodeCall+commandCodeResult(result))
	}
}

// Command Code opens a failed command's result with "Exit code: N", or "Exit
// code: N (<what it means>)" for a code it knows (formatShellCommandResult),
// and sets no is_error; the reader never joined a result to its call (#4539).
func TestCommandCodeFailedCommandKeepsItsExitCode(t *testing.T) {
	runExitRows(t, []exitRow{
		{"failed", "commandcode", commandCodeExitStore("Exit code: 1\n--- FAIL: TestRetry (0.00s)\nFAIL"), "$ go test ./...  → exit 1"},
		{"failed, explained", "commandcode", commandCodeExitStore("Exit code: 1 (No matches found)"), "$ go test ./...  → exit 1"},
		{"clean", "commandcode", commandCodeExitStore("ok  \tproj\t0.01s"), "$ go test ./..."},
		{"quoting", "commandcode", commandCodeExitStore("ok\nExit code: 1"), "$ go test ./..."},
	})
}

// A result answers the call before it, once. Command Code's ids read like
// call_1, and an id handed out again must not carry a later failure back to
// an earlier run that came back clean.
func TestAReusedCallIDStampsOnlyItsOwnRun(t *testing.T) {
	dir := t.TempDir()
	second := strings.ReplaceAll(strings.ReplaceAll(commandCodeCall, `"go test ./..."`, `"go vet ./..."`), `"id":"a1"`, `"id":"a2"`)
	p := writeExitFixture(t, filepath.Join(dir, "-tmp-proj", "s1.jsonl"),
		`{"type":"session","version":3,"id":"s1","timestamp":"2026-10-01T20:09:14.839Z","cwd":"/tmp/proj"}`+"\n"+
			commandCodeCall+commandCodeResult("ok  \tproj\t0.01s")+second+commandCodeResult("Exit code: 1\nvet: retry.go:3: unreachable code"))
	got := commandsOf(parseKindForTest(t, "commandcode", p))
	want := []string{"$ go test ./...", "$ go vet ./...  → exit 1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("commands = %q, want %q", got, want)
	}
}
