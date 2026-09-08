package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Grok records what a tool ran in the tool_call event's rawInput; the reader
// kept the title and the output and dropped the input, so no Grok session ever
// yielded a command or a file record (#3285). Shape from the real store.
func TestGrokToolCallInputBecomesCommandAndFileRecords(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GROK_ROOT", root)
	dir := filepath.Join(root, "sessions", "%2Fw%2Fapp", "01a03432-4d92-7243-ab6c-8cdd1c697738")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rows := []string{
		`{"timestamp":1787582100,"method":"session/update","params":{"sessionId":"01a03432-4d92-7243-ab6c-8cdd1c697738","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"run the grok tests"}},"_meta":{"promptId":"p1","agentTimestampMs":1787582100000}}}`,
		`{"timestamp":1787582112,"method":"session/update","params":{"sessionId":"01a03432-4d92-7243-ab6c-8cdd1c697738","update":{"sessionUpdate":"tool_call","toolCallId":"call-1","title":"run_terminal_command","rawInput":{"command":"go test ./internal/sources/ -run Grok"},"_meta":{"x.ai/tool":{"version":1,"name":"run_terminal_command","kind":"run_terminal_command","namespace":"grok_build","label":"Run Terminal Command","read_only":false}}},"_meta":{"promptId":"p1","agentTimestampMs":1787582112336}}}`,
		`{"timestamp":1787582115,"method":"session/update","params":{"sessionId":"01a03432-4d92-7243-ab6c-8cdd1c697738","update":{"sessionUpdate":"tool_call_update","toolCallId":"call-1","status":"completed","content":[{"type":"content","content":{"type":"text","text":"ok  \tgithub.com/x/y\t0.4s"}}]},"_meta":{"promptId":"p1","agentTimestampMs":1787582115000}}}`,
		`{"timestamp":1787582120,"method":"session/update","params":{"sessionId":"01a03432-4d92-7243-ab6c-8cdd1c697738","update":{"sessionUpdate":"tool_call","toolCallId":"call-2","title":"read_file","rawInput":{"target_file":"/w/app/internal/sources/grok.go"},"_meta":{"x.ai/tool":{"version":1,"name":"read_file","kind":"read_file","namespace":"grok_build","label":"Read File","read_only":true}}},"_meta":{"promptId":"p1","agentTimestampMs":1787582120000}}}`,
	}
	if err := os.WriteFile(filepath.Join(dir, "updates.jsonl"), []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGrokFile(filepath.Join(dir, "updates.jsonl"))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var cmds, files []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleCommand:
			cmds = append(cmds, m.Text)
		case RoleFiles:
			files = append(files, m.Text)
		}
	}
	if len(cmds) != 1 || cmds[0] != "$ go test ./internal/sources/ -run Grok" {
		t.Errorf("commands = %q, want the run_terminal_command input", cmds)
	}
	if len(files) != 1 || !strings.Contains(files[0], "/w/app/internal/sources/grok.go") {
		t.Errorf("files = %q, want the read_file target", files)
	}
}
