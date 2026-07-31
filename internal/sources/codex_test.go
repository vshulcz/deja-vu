package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Codex has no Read or Edit tool: it reads files with shell commands and makes
// every change through apply_patch. So the file records come out of the patch,
// and so does the span `deja restore` hands back.
func TestCodexRolloutWorkRecords(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rollout-2026-07-31T00-00-00-abc.jsonl")
	lines := []string{
		`{"timestamp":"2026-07-31T00:00:00Z","type":"session_meta","payload":{"session_id":"abc","cwd":"/w/app"}}`,
		`{"timestamp":"2026-07-31T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"fix the greeting"}]}}`,
		`{"timestamp":"2026-07-31T00:00:02Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{\"cmd\":\"go test ./...\",\"workdir\":\"/w/app\"}"}}`,
		`{"timestamp":"2026-07-31T00:00:03Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"Chunk ID: 5e9c5d\nWall time: 0.1 seconds\nProcess exited with code 2\nOriginal token count: 25\nOutput:\nFAIL\tgithub.com/x/app\n"}}`,
		`{"timestamp":"2026-07-31T00:00:04Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c2","arguments":"{\"cmd\":\"ls -la\"}"}}`,
		`{"timestamp":"2026-07-31T00:00:05Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"c3","input":"*** Begin Patch\n*** Update File: main.go\n@@\n-\tprintln(\"hello\")\n+\tprintln(\"goodbye\")\n*** Add File: notes.txt\n+alpha\n*** End Patch\n"}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodexRollout(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	// The outcome arrives in a separate record joined by call_id; without the
	// join the command reads as though it succeeded.
	if len(byRole[RoleCommand]) != 1 || byRole[RoleCommand][0] != "$ go test ./...  → exit 2" {
		t.Errorf("commands = %q, want the meaningful one annotated with its exit", byRole[RoleCommand])
	}
	// Codex frames every result with a chunk id, a wall time and a token count.
	if len(byRole[RoleToolOutput]) != 1 || strings.Contains(byRole[RoleToolOutput][0], "Chunk ID") ||
		!strings.Contains(byRole[RoleToolOutput][0], "FAIL\tgithub.com/x/app") {
		t.Errorf("tool output = %q, want the body without the framing", byRole[RoleToolOutput])
	}
	files := strings.Join(byRole[RoleFiles], "\n")
	for _, want := range []string{filepath.Join("/w/app", "main.go"), filepath.Join("/w/app", "notes.txt")} {
		if !strings.Contains(files, want) {
			t.Errorf("files = %q, want %q resolved against the session cwd", files, want)
		}
	}
	if len(byRole[RoleEdit]) != 1 || !strings.Contains(byRole[RoleEdit][0], `println("hello")`) ||
		strings.Contains(byRole[RoleEdit][0], "goodbye") {
		t.Errorf("edit = %q, want only the bytes that stopped existing", byRole[RoleEdit])
	}
}

func TestCodexWorkRecordsRespectTheirSwitches(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rollout-2026-07-31T00-00-00-off.jsonl")
	lines := []string{
		`{"timestamp":"2026-07-31T00:00:00Z","type":"session_meta","payload":{"session_id":"off","cwd":"/w/app"}}`,
		`{"timestamp":"2026-07-31T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"keep one message"}]}}`,
		`{"timestamp":"2026-07-31T00:00:02Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{\"cmd\":\"go build ./...\"}"}}`,
		`{"timestamp":"2026-07-31T00:00:03Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"Process exited with code 0\nOutput:\nok\n"}}`,
		`{"timestamp":"2026-07-31T00:00:04Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":"*** Begin Patch\n*** Update File: main.go\n@@\n-old\n+new\n*** End Patch\n"}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_PATHS", "0")
	t.Setenv("DEJA_INDEX_EDITS", "0")
	t.Setenv("DEJA_INDEX_COMMANDS", "0")
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	ss, err := ParseCodexRollout(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleFiles, RoleEdit, RoleCommand, RoleToolOutput:
			t.Fatalf("a switched-off record was still written: %s %q", m.Role, m.Text)
		}
	}
}

// A rollout record without a call_id is not an error. Asserting the id bare
// turned one into a panic that took the whole parse of the session down, and
// no test caught it because every call in the local corpus had an id.
func TestCodexOutputWithoutCallIDDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rollout-2026-07-31T00-00-00-nc.jsonl")
	lines := []string{
		`{"timestamp":"2026-07-31T00:00:00Z","type":"session_meta","payload":{"session_id":"nc","cwd":"/w"}}`,
		`{"timestamp":"2026-07-31T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"hi"}]}}`,
		`{"timestamp":"2026-07-31T00:00:02Z","type":"response_item","payload":{"type":"function_call_output","output":"Process exited with code 1\nOutput:\nboom\n"}}`,
		`{"timestamp":"2026-07-31T00:00:03Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"go test ./...\"}"}}`,
		`{"timestamp":"2026-07-31T00:00:04Z","type":"response_item","payload":{"type":"function_call_output","call_id":123,"output":"Exit code: 2\nOutput:\nno\n"}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodexRollout(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	// The output still lands; only the exit annotation is lost, which is the
	// right degradation for a record that names no call.
	var out int
	for _, m := range ss[0].Messages {
		if m.Role == RoleToolOutput {
			out++
		}
	}
	if out != 2 {
		t.Fatalf("tool output records = %d, want 2", out)
	}
}
