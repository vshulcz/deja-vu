package sources

import (
	"github.com/vshulcz/deja-vu/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Roo and the legacy Cline extension name their tools execute_command,
// read_file, write_to_file, apply_diff / replace_in_file; the readers took the
// assistant's prose and none of the calls, so no command or file record ever
// came out of them (#3295). Shape from Roo's own Task.ts.
func writeRooTask(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "tasks", "1788845325718")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `[
	 {"role":"user","content":[{"type":"text","text":"<task>\nrun go test and fix whatever fails\n</task>"}]},
	 {"role":"assistant","content":[{"type":"text","text":"Running the tests."},{"type":"tool_use","id":"t1","name":"execute_command","input":{"command":"go test ./..."}}]},
	 {"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"Exit code: 1\nOutput:\npkg/parser.go:42:9: undefined: frobnicateWidget\nFAIL"}]},
	 {"role":"assistant","content":[{"type":"text","text":"Renaming the call."},{"type":"tool_use","id":"t2","name":"read_file","input":{"path":"pkg/parser.go"}}]},
	 {"role":"assistant","content":[{"type":"tool_use","id":"t3","name":"apply_diff","input":{"path":"pkg/parser.go","diff":"<<<<<<< SEARCH\n\tout := frobnicateWidget(w)\n=======\n\tout := frobnicate(w)\n>>>>>>> REPLACE"}}]}
	]`
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRooToolCallsBecomeCommandAndFileRecords(t *testing.T) {
	path := writeRooTask(t, t.TempDir())
	ss, err := ParseRooTask(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	checkRooWork(t, "roo", ss[0].Messages)
	ss, err = parseClineLegacyTask(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("legacy parse: %v, %d sessions", err, len(ss))
	}
	checkRooWork(t, "cline legacy", ss[0].Messages)
}

func checkRooWork(t *testing.T, reader string, ms []model.Message) {
	t.Helper()
	var cmds, files []string
	for _, m := range ms {
		switch m.Role {
		case RoleCommand:
			cmds = append(cmds, m.Text)
		case RoleFiles:
			files = append(files, m.Text)
		}
	}
	if len(cmds) != 1 || cmds[0] != "$ go test ./..." {
		t.Errorf("%s: commands = %q, want the execute_command", reader, cmds)
	}
	if len(files) == 0 || !strings.Contains(strings.Join(files, "\n"), "pkg/parser.go") {
		t.Errorf("%s: files = %q, want pkg/parser.go", reader, files)
	}
}
