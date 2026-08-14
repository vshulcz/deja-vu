package sources

import (
	"strings"
	"testing"
)

// One session that works: it speaks, edits a file, runs a command that fails,
// reads a file, and closes. Shapes are the ones the Copilot CLI writes —
// lowercase tool names, `path` rather than `file_path`, `old_str` rather than
// `old_string`, and results under `result.content`.
func copilotWorkLines() []string {
	return []string{
		`{"type":"session.start","data":{"sessionId":"s1","startTime":"2026-07-20T05:13:54.687Z","context":{"cwd":"/Users/x/coding/gateway"}},"timestamp":"2026-07-20T05:13:54.707Z"}`,
		`{"type":"user.message","data":{"content":"the build is broken"},"timestamp":"2026-07-20T05:13:58.653Z"}`,
		`{"type":"assistant.message","data":{"content":"looking at it"},"timestamp":"2026-07-20T05:14:00.973Z"}`,
		`{"type":"tool.execution_start","data":{"toolName":"edit","arguments":{"path":"/Users/x/coding/gateway/main.go","old_str":"println(\"hello\")","new_str":"println(\"goodbye\")"}},"timestamp":"2026-07-20T05:14:01.100Z"}`,
		`{"type":"tool.execution_complete","data":{"success":true,"result":{"content":"1 replacement"}},"timestamp":"2026-07-20T05:14:01.200Z"}`,
		`{"type":"tool.execution_start","data":{"toolName":"bash","arguments":{"command":"go vet ./..."}},"timestamp":"2026-07-20T05:14:02.100Z"}`,
		`{"type":"tool.execution_complete","data":{"success":false,"result":{"content":"pattern ./...: directory prefix . does not contain main module"}},"timestamp":"2026-07-20T05:14:02.900Z"}`,
		`{"type":"tool.execution_start","data":{"toolName":"read","arguments":{"path":"/Users/x/coding/gateway/go.mod"}},"timestamp":"2026-07-20T05:14:03.100Z"}`,
		`{"type":"tool.execution_start","data":{"toolName":"todo","arguments":{"items":[]}},"timestamp":"2026-07-20T05:14:03.500Z"}`,
		`{"type":"session.shutdown","data":{},"timestamp":"2026-07-20T05:14:04.029Z"}`,
	}
}

func TestParseCopilotEmitsWorkRecords(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	p := writeCopilotFixture(t, root, "s1", copilotWorkLines())
	ss, err := ParseCopilotFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}

	if got := byRole[RoleCommand]; len(got) != 1 || got[0] != "$ go vet ./..." {
		t.Errorf("commands = %q, want the vet run alone — todo carries none", got)
	}
	wantFiles := []string{"/Users/x/coding/gateway/main.go", "/Users/x/coding/gateway/go.mod"}
	if got := byRole[RoleFiles]; strings.Join(got, ",") != strings.Join(wantFiles, ",") {
		t.Errorf("files = %q, want the edit and the read in order", got)
	}
	// old_str, not old_string: reading the wrong key stores the path with no
	// span, which is the record silently losing what it is for.
	if got := byRole[RoleEdit]; len(got) != 1 ||
		got[0] != "/Users/x/coding/gateway/main.go\nprintln(\"hello\")" ||
		strings.Contains(strings.Join(got, ""), "goodbye") {
		t.Errorf("edit = %q, want only the bytes that stopped existing", got)
	}
	// The failure is the point: it is what a later search reaches for.
	wantOut := []string{"1 replacement", "pattern ./...: directory prefix . does not contain main module"}
	if got := byRole[RoleToolOutput]; strings.Join(got, "|") != strings.Join(wantOut, "|") {
		t.Errorf("tool output = %q, want both results including the error", got)
	}
}

// With the switches off the stream is what it was before tool events were
// read, so nobody's existing index changes shape under them.
func TestParseCopilotWorkRecordsSwitchOff(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	t.Setenv("DEJA_INDEX_COMMANDS", "0")
	t.Setenv("DEJA_INDEX_PATHS", "0")
	t.Setenv("DEJA_INDEX_EDITS", "0")
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	p := writeCopilotFixture(t, root, "s1", copilotWorkLines())
	ss, err := ParseCopilotFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v", err)
	}
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleCommand, RoleFiles, RoleEdit, RoleToolOutput:
			t.Fatalf("record %q survived its switch", m.Role)
		}
	}
	if len(ss[0].Messages) != 2 {
		t.Fatalf("want the two spoken turns back, got %d", len(ss[0].Messages))
	}
}
