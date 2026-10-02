package sources

import (
	"strings"
	"testing"
)

// crushJoinRecords builds one session whose rows are, in order, the calls and
// results given as {kind, id, tool, payload, isError}.
func crushJoinRecords(t *testing.T, session string, rows [][]any) []string {
	t.Helper()
	sql := "insert into sessions values ('" + session + "',null,'retry',2,0,0,0.0,1784282499,1784282400,null,null);\n"
	for i, r := range rows {
		kind, id, tool, payload, isErr := r[0].(string), r[1].(string), r[2].(string), r[3].(string), r[4].(bool)
		at := int64(1784282400 + i)
		if kind == "call" {
			sql += crushInsert(t, "m"+itoa(i), session, "assistant", at, []any{
				map[string]any{"type": "tool_call", "data": map[string]any{"id": id, "name": tool, "input": payload, "finished": true}}})
		} else {
			sql += crushInsert(t, "m"+itoa(i), session, "tool", at, []any{
				map[string]any{"type": "tool_result", "data": map[string]any{"tool_call_id": id, "name": tool, "content": payload, "is_error": isErr}}})
		}
	}
	ss, err := ParseCrushDB(crushStore(t, "proj", sql))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range ss {
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
	return got
}

// A result answers the one call before it. A provider that numbers its calls
// per turn hands out the same id again, and a later refusal or failure under
// that id must not reach back to an earlier call that went through.
func TestCrushJoinReusedCallID(t *testing.T) {
	edit := `{"file_path":"/tmp/proj/retry.go","old_string":"\tfor {","new_string":"\tfor i := 0; i < 3; i++ {"}`
	got := crushJoinRecords(t, "reused", [][]any{
		{"call", "call_0", "edit", edit, false},
		{"result", "call_0", "edit", "Content replaced in file: /tmp/proj/retry.go", false},
		{"call", "call_0", "bash", `{"command":"go test ./retry"}`, false},
		{"result", "call_0", "bash", "ok  retry 0.01s\n\n<cwd>/tmp/proj</cwd>", false},
		{"call", "call_0", "edit", edit, false},
		{"result", "call_0", "edit", "you must read the file before editing it. Use the View tool first", true},
		{"call", "call_0", "bash", `{"command":"go vet ./retry"}`, false},
		{"result", "call_0", "bash", "boom\nExit code 1\n\n<cwd>/tmp/proj</cwd>", false},
	})
	want := []string{
		"files /tmp/proj/retry.go", "edit /tmp/proj/retry.go\n\tfor {", "wrote",
		"command go test ./retry",
		"files /tmp/proj/retry.go",
		"command go vet ./retry  → exit 1",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("records =\n%q\nwant\n%q", got, want)
	}
}

// Crush appends its <cwd> tag after everything the command printed; a tag in
// the command's own output is output, and the exit line still sits above the
// appended one.
func TestCrushExitWhenOutputHoldsACwdTag(t *testing.T) {
	got := crushJoinRecords(t, "cwdtag", [][]any{
		{"call", "c1", "bash", `{"command":"make check"}`, false},
		{"result", "c1", "bash", "<cwd>/old</cwd> from last run\ncat: more.txt: No such file\nExit code 1\n\n<cwd>/tmp/proj</cwd>", false},
	})
	want := []string{"command make check  → exit 1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("records = %q, want %q", got, want)
	}
}
