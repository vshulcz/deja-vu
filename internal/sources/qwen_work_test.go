package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Qwen files its work as functionCall and functionResponse parts beside the
// text parts, so the reader that kept only text walked past every command a
// session ran.
const qwenWorkSession = `{"type":"user","timestamp":"2026-07-20T05:13:58Z","message":{"role":"user","parts":[{"text":"the build is broken"}]}}
{"type":"assistant","timestamp":"2026-07-20T05:14:00Z","message":{"role":"model","parts":[{"text":"looking at it"},{"functionCall":{"name":"run_shell_command","args":{"command":"go vet ./...","working_dir":"/w/app"}}}]}}
{"type":"assistant","timestamp":"2026-07-20T05:14:01Z","message":{"role":"user","parts":[{"functionResponse":{"name":"run_shell_command","response":{"output":"pattern ./...: directory prefix . does not contain main module"}}}]}}
{"type":"assistant","timestamp":"2026-07-20T05:14:02Z","message":{"role":"model","parts":[{"functionCall":{"name":"read_file","args":{"file_path":"/w/app/go.mod"}}}]}}
{"type":"assistant","timestamp":"2026-07-20T05:14:03Z","message":{"role":"model","parts":[{"functionCall":{"name":"replace","args":{"file_path":"/w/app/main.go","old_string":"println(\"hello\")","new_string":"println(\"goodbye\")"}}}]}}
{"type":"assistant","timestamp":"2026-07-20T05:14:04Z","message":{"role":"model","parts":[{"functionCall":{"name":"web_fetch","args":{"url":"https://example.invalid"}}}]}}
`

func writeQwenWork(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("DEJA_QWEN_ROOT", root)
	dir := filepath.Join(root, "projects", "app", "chats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "s1.jsonl")
	if err := os.WriteFile(p, []byte(qwenWorkSession), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseQwenEmitsWorkRecords(t *testing.T) {
	ss, err := ParseQwenFile(writeQwenWork(t))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	if got := byRole[RoleCommand]; len(got) != 1 || got[0] != "$ go vet ./..." {
		t.Errorf("commands = %q, want the vet run", got)
	}
	wantFiles := []string{"/w/app/go.mod", "/w/app/main.go"}
	if got := byRole[RoleFiles]; strings.Join(got, "|") != strings.Join(wantFiles, "|") {
		t.Errorf("files = %q, want the read and the replace", got)
	}
	if got := byRole[RoleEdit]; len(got) != 1 ||
		got[0] != "/w/app/main.go\nprintln(\"hello\")" ||
		strings.Contains(strings.Join(got, ""), "goodbye") {
		t.Errorf("edit = %q, want only the bytes that stopped existing", got)
	}
	if got := byRole[RoleToolOutput]; len(got) != 1 ||
		!strings.Contains(got[0], "does not contain main module") {
		t.Errorf("tool output = %q, want the failure the command hit", got)
	}
	// A fetch is neither a file nor a command on this machine.
	for _, texts := range byRole {
		if strings.Contains(strings.Join(texts, ""), "example.invalid") {
			t.Error("a web fetch became a work record")
		}
	}
}

func TestParseQwenWorkRecordsSwitchOff(t *testing.T) {
	t.Setenv("DEJA_INDEX_COMMANDS", "0")
	t.Setenv("DEJA_INDEX_PATHS", "0")
	t.Setenv("DEJA_INDEX_EDITS", "0")
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	ss, err := ParseQwenFile(writeQwenWork(t))
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
