package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// kiroRoles collects the text of every message under a role.
func kiroRoles(s model.Session) map[string][]string {
	out := map[string][]string{}
	for _, m := range s.Messages {
		out[m.Role] = append(out[m.Role], m.Text)
	}
	return out
}

func kiroHas(texts []string, want string) bool {
	for _, t := range texts {
		if strings.Contains(t, want) {
			return true
		}
	}
	return false
}

// The TUI writes the work beside the talk: toolUse parts in an
// AssistantMessage, toolResult parts in a ToolResults record. Read as text
// only, a Kiro CLI session had no command, no file and no tool output (#4299).
func TestKiroCLIReadsToolCallsAndResults(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "11111111-2222-4333-8444-555555555555"
	header := `{"session_id":"` + id + `","cwd":"/tmp/proj"}`
	lines := `{"version":"v1","kind":"Prompt","data":{"message_id":"m1","content":[{"kind":"text","data":"fix the retry loop"}],"meta":{"timestamp":1790848800}}}
{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m2","content":[{"kind":"text","data":"Checking the loop."},{"kind":"toolUse","data":{"toolUseId":"t1","name":"shell","input":{"command":"go test ./retry"}}},{"kind":"toolUse","data":{"toolUseId":"t2","name":"write","input":{"command":"create","path":"/tmp/proj/retry.go","content":"package retry\n\nconst maxAttemptsBeforeGivingUp = 3\n"}}}]}}
{"version":"v1","kind":"ToolResults","data":{"message_id":"m3","content":[{"kind":"toolResult","data":{"toolUseId":"t1","content":[{"kind":"json","data":{"exit_status":"exit status: 0","stdout":"ok  retry 0.01s","stderr":""}}],"status":"success"}},{"kind":"toolResult","data":{"toolUseId":"t2","content":[{"kind":"text","data":"Successfully created /tmp/proj/retry.go"}],"status":"success"}}]}}
{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m4","content":[{"kind":"text","data":"Now the cap."},{"kind":"toolUse","data":{"toolUseId":"t3","name":"write","input":{"command":"strReplace","path":"/tmp/proj/retry.go","oldStr":"const maxAttemptsBeforeGivingUp = 3","newStr":"const maxAttemptsBeforeGivingUp = 5"}}},{"kind":"toolUse","data":{"toolUseId":"t4","name":"read","input":{"operations":[{"mode":"Line","path":"/tmp/proj/retry_test.go"},{"mode":"Directory","path":"/tmp/proj"}]}}}]}}
{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m4","content":[{"kind":"text","data":" Done."}]}}
{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m5","content":[{"kind":"text","data":"The loop now stops after five attempts."}]}}
`
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(header), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseKiroCLIFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	got := kiroRoles(ss[0])
	if !kiroHas(got[RoleCommand], "$ go test ./retry") {
		t.Errorf("the shell call is not a command: %q", got[RoleCommand])
	}
	if !kiroHas(got[RoleFiles], "/tmp/proj/retry.go") || !kiroHas(got[RoleFiles], "/tmp/proj/retry_test.go") {
		t.Errorf("the written and read files are not recorded: %q", got[RoleFiles])
	}
	for _, rec := range got[RoleFiles] {
		for _, p := range strings.Split(rec, "\n") {
			if p == "/tmp/proj" {
				t.Errorf("a directory listing was recorded as a file: %q", got[RoleFiles])
			}
		}
	}
	if len(got[RoleWrote]) == 0 {
		t.Errorf("the created file's content is not recorded: %+v", ss[0].Messages)
	}
	if !kiroHas(got[RoleEdit], "const maxAttemptsBeforeGivingUp = 3") {
		t.Errorf("the strReplace span is not recorded: %q", got[RoleEdit])
	}
	if !kiroHas(got[RoleToolOutput], "ok  retry") || !kiroHas(got[RoleToolOutput], "Successfully created") {
		t.Errorf("the tool results are missing: %q", got[RoleToolOutput])
	}
	// The streamed reply still joins, with the work records in between.
	if !kiroHas(got["assistant"], "Now the cap. Done.") {
		t.Errorf("the streamed reply was not joined: %q", got["assistant"])
	}
	if len(got["user"]) != 1 {
		t.Errorf("user turns = %q, want the prompt alone", got["user"])
	}
}
