package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One session that works, in the shapes cline's own tool schemas declare:
// `run_commands` takes a list under `commands`, `read_files` a list of read
// requests under `files`, and the editor names the replaced text `old_text`.
// All three differ from every other harness, and reading the singular keys
// indexed none of it.
const clineWorkSession = `{"version":1,"agent":"lead","sessionId":"session_w","messages":[
{"role":"user","content":[{"type":"text","text":"the build is broken"}],"ts":1767225600000},
{"role":"assistant","content":[
  {"type":"text","text":"looking at it"},
  {"type":"tool_use","id":"c1","name":"run_commands","input":{"commands":["go vet ./...","go test ./..."]}}
],"ts":1767225601000},
{"role":"user","content":[
  {"type":"tool_result","tool_use_id":"c1","content":"pattern ./...: directory prefix . does not contain main module"}
],"ts":1767225602000},
{"role":"assistant","content":[
  {"type":"tool_use","id":"c2","name":"read_files","input":{"files":[{"path":"/w/app/go.mod"},{"path":"/w/app/main.go"}]}}
],"ts":1767225603000},
{"role":"assistant","content":[
  {"type":"tool_use","id":"c3","name":"editor","input":{"path":"/w/app/main.go","old_text":"println(\"hello\")","new_text":"println(\"goodbye\")"}}
],"ts":1767225604000},
{"role":"assistant","content":[
  {"type":"tool_use","id":"c4","name":"ask_question","input":{"question":"proceed?"}}
],"ts":1767225605000}
]}`

func writeClineWork(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sess := filepath.Join(dir, "session_w")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(sess, "session_w.messages.json")
	if err := os.WriteFile(p, []byte(clineWorkSession), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseClineEmitsWorkRecords(t *testing.T) {
	ss, err := ParseClineFile(writeClineWork(t))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}

	// Both commands from one call: the singular key would have found neither.
	want := []string{"$ go vet ./...", "$ go test ./..."}
	if got := byRole[RoleCommand]; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("commands = %q, want both from the one call", got)
	}
	// Both files from the read list, plus the edited one.
	wantFiles := []string{"/w/app/go.mod\n/w/app/main.go", "/w/app/main.go"}
	if got := byRole[RoleFiles]; strings.Join(got, "|") != strings.Join(wantFiles, "|") {
		t.Errorf("files = %q, want the read list and the edit", got)
	}
	// old_text, not old_string: the wrong key stores a path with no span.
	if got := byRole[RoleEdit]; len(got) != 1 ||
		got[0] != "/w/app/main.go\nprintln(\"hello\")" ||
		strings.Contains(strings.Join(got, ""), "goodbye") {
		t.Errorf("edit = %q, want only the bytes that stopped existing", got)
	}
	if got := byRole[RoleToolOutput]; len(got) != 1 ||
		!strings.Contains(got[0], "does not contain main module") {
		t.Errorf("tool output = %q, want the failure the command hit", got)
	}
	// A call that is neither a file, a command nor an edit contributes nothing.
	for _, texts := range byRole {
		if strings.Contains(strings.Join(texts, ""), "proceed?") {
			t.Error("a question tool became a work record")
		}
	}
}

func TestParseClineWorkRecordsSwitchOff(t *testing.T) {
	t.Setenv("DEJA_INDEX_COMMANDS", "0")
	t.Setenv("DEJA_INDEX_PATHS", "0")
	t.Setenv("DEJA_INDEX_EDITS", "0")
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	ss, err := ParseClineFile(writeClineWork(t))
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
