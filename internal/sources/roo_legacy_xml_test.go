package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Before native tool calling, Roo (v3.20, for one) and Cline kept a tool call
// as XML inside the assistant's text block and its result as user text blocks
// headed `[execute_command for '...'] Result:`. The readers took tool_use
// blocks only, so a task of that era had no command, files or edit record and
// its command output was indexed as the user's words (#4424). Shape from
// Roo's Task.ts and Cline's pushToolResult.
func TestRooLegacyXMLCallsLeaveRecords(t *testing.T) {
	const env = "<environment_details>\n# VSCode Visible Files\nretry/backoff.go\n</environment_details>"
	turn := func(role string, texts ...string) map[string]any {
		var blocks []any
		for _, t := range texts {
			blocks = append(blocks, map[string]any{"type": "text", "text": t})
		}
		return map[string]any{"role": role, "content": blocks}
	}
	body, err := json.Marshal([]any{
		turn("user", "<task>\nthe backoff test overflows\n</task>", env),
		turn("assistant", "Running the tests.\n<execute_command>\n<command>go test ./retry/...</command>\n</execute_command>"),
		turn("user", "[execute_command for 'go test ./retry/...'] Result:",
			"Command executed in terminal within working directory '/w'. Exit code: 1\nOutput:\nFAIL retry_test.go:12 backoff overflow", env),
		turn("assistant", "<read_file>\n<args>\n  <file>\n    <path>retry/backoff.go</path>\n  </file>\n  <file>\n    <path>retry/clock.go</path>\n  </file>\n</args>\n</read_file>"),
		turn("user", "[read_file for 'retry/backoff.go, retry/clock.go'] Result:", "<files><file><path>retry/backoff.go</path><content>1 | package retry</content></file></files>"),
		turn("assistant", "Capping the shift.\n<apply_diff>\n<path>retry/backoff.go</path>\n<diff>\n<<<<<<< SEARCH\n:start_line:12\n-------\n\treturn base << attempt\n=======\n\treturn base << min(attempt, maxShiftForBackoff)\n>>>>>>> REPLACE\n</diff>\n</apply_diff>"),
		turn("user", "[apply_diff for 'retry/backoff.go'] Result:", "Changes successfully applied to retry/backoff.go", env),
		turn("assistant", "<ask_followup_question>\n<question>Cap at 30 or 60?</question>\n</ask_followup_question>"),
		turn("user", "[ask_followup_question for 'Cap at 30 or 60?'] Result:", "<answer>\nsixty, the old ceiling\n</answer>"),
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "tasks", "d0000000-0000-7000-8000-000000000001")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	h, ok := HashWrittenLine("\treturn base << min(attempt, maxShiftForBackoff)")
	if !ok {
		t.Fatal("the new line is too short to be evidence")
	}
	for _, reader := range []struct {
		name  string
		parse func(string) ([]model.Session, error)
	}{
		{"roo", ParseRooTask},
		{"kilocode", ParseKiloTask},
		{"cline legacy", parseClineLegacyTask},
	} {
		ss, err := reader.parse(path)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s parse: %v, %d sessions", reader.name, err, len(ss))
		}
		by := map[string][]string{}
		for _, m := range ss[0].Messages {
			by[m.Role] = append(by[m.Role], m.Text)
		}
		if got := by[RoleCommand]; len(got) != 1 || got[0] != "$ go test ./retry/..." {
			t.Errorf("%s: commands = %q", reader.name, got)
		}
		files := strings.Join(by[RoleFiles], "\n")
		for _, f := range []string{"retry/backoff.go", "retry/clock.go"} {
			if !strings.Contains(files, f) {
				t.Errorf("%s: no files record for %s: %q", reader.name, f, by[RoleFiles])
			}
		}
		if got := by[RoleEdit]; len(got) != 1 || got[0] != "retry/backoff.go\n\treturn base << attempt" {
			t.Errorf("%s: edits = %q", reader.name, got)
		}
		var wrote bool
		for _, rec := range by[RoleWrote] {
			if p, has := WroteRecordHas(rec, h); has && p == "retry/backoff.go" {
				wrote = true
			}
		}
		if !wrote {
			t.Errorf("%s: the written line is in no record: %q", reader.name, by[RoleWrote])
		}
		out := strings.Join(by[RoleToolOutput], "\n")
		if !strings.Contains(out, "FAIL retry_test.go:12 backoff overflow") || !strings.Contains(out, "Changes successfully applied") {
			t.Errorf("%s: tool output = %q", reader.name, by[RoleToolOutput])
		}
		// The person's words are the task and the answer they gave; neither
		// a result header nor a result body is one of them.
		users := strings.Join(by["user"], "\n---\n")
		if strings.Contains(users, "] Result:") || strings.Contains(users, "FAIL retry_test.go") || strings.Contains(users, "successfully applied") {
			t.Errorf("%s: a tool result was indexed as the user's words: %q", reader.name, by["user"])
		}
		if !strings.Contains(users, "the backoff test overflows") || !strings.Contains(users, "sixty, the old ceiling") {
			t.Errorf("%s: the user's own words are missing: %q", reader.name, by["user"])
		}
	}
}

// A task of the native era calls its tools as tool_use blocks, so XML in an
// assistant's text there is something it showed, not something the client
// ran: a reply explaining the old format, a snippet in a code fence. And a
// result of the XML era that carries the person's feedback keeps that
// feedback as their words.
func TestRooXMLOnlyInXMLEraTasks(t *testing.T) {
	write := func(turns []any) string {
		body, err := json.Marshal(turns)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(t.TempDir(), "tasks", "d0000000-0000-7000-8000-000000000002")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "api_conversation_history.json")
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	text := func(role string, texts ...string) map[string]any {
		var blocks []any
		for _, s := range texts {
			blocks = append(blocks, map[string]any{"type": "text", "text": s})
		}
		return map[string]any{"role": role, "content": blocks}
	}
	native := write([]any{
		text("user", "<task>\nhow did the old roo call tools?\n</task>"),
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "t1", "name": "read_file", "input": map[string]any{"path": "README.md"}},
		}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "# readme"},
		}},
		text("assistant", "It wrote the call as XML in its reply:\n```xml\n<execute_command>\n<command>go test ./retry/... -run Backoff</command>\n</execute_command>\n```"),
	})
	denied := write([]any{
		text("user", "<task>\nclean the build\n</task>"),
		// The client of that era ran one call per message and answered the
		// rest "was not executed because a tool has already been used".
		text("assistant", "<execute_command>\n<command>rm -rf build</command>\n</execute_command>\n<execute_command>\n<command>go build ./... -o build/out</command>\n</execute_command>"),
		text("user", "[execute_command for 'rm -rf build'] Result:",
			"The user denied this operation and provided the following feedback:\n<feedback>\nkeep build/cache, only drop build/out\n</feedback>"),
	})
	for _, reader := range []struct {
		name  string
		parse func(string) ([]model.Session, error)
	}{
		{"roo", ParseRooTask},
		{"kilocode", ParseKiloTask},
		{"cline legacy", parseClineLegacyTask},
	} {
		ss, err := reader.parse(native)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s parse: %v, %d sessions", reader.name, err, len(ss))
		}
		for _, m := range ss[0].Messages {
			if m.Role == RoleCommand {
				t.Errorf("%s: XML shown in a native-era reply became a command: %q", reader.name, m.Text)
			}
		}
		ss, err = reader.parse(denied)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s parse: %v, %d sessions", reader.name, err, len(ss))
		}
		var users []string
		for _, m := range ss[0].Messages {
			if m.Role == "user" {
				users = append(users, m.Text)
			}
			if m.Role == RoleCommand && strings.Contains(m.Text, "go build") {
				t.Errorf("%s: the second call of a message, which the client never ran, became a command", reader.name)
			}
		}
		if !strings.Contains(strings.Join(users, "\n"), "keep build/cache, only drop build/out") {
			t.Errorf("%s: the feedback the person typed is not among their words: %q", reader.name, users)
		}
	}
}
