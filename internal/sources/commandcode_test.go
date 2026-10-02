package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Command Code 1.73 writes a `session` header and then one `message` envelope
// per line, with Claude's content blocks inside and its own tool names. The
// reader looked for `role` at the top level and indexed none of it (#4370).
// The lines are the ones command-code 1.73.4 wrote, cut down.
const commandCodeV3 = `{"type":"session","version":3,"id":"1583a430-5538-475d-9190-0d726c7a374f","timestamp":"2026-10-01T20:09:14.839Z","cwd":"/private/tmp/proj-cc"}
{"type":"message","id":"f82f93b8","parentId":null,"timestamp":"2026-10-01T20:09:15.324Z","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}],"meta":{"source":"user"}}}
{"type":"message","id":"18f845f1","parentId":"f82f93b8","timestamp":"2026-10-01T20:09:15.324Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call_baf6","name":"shell_command","input":{"command":"git status --short && cat retry.cfg"}}],"meta":{"source":"model"}},"model":"shim/luna"}
{"type":"message","id":"3fa018fa","parentId":"18f845f1","timestamp":"2026-10-01T20:09:15.324Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_baf6","content":[{"type":"text","text":"?? retry.cfg\nretry = 3\n"}]}],"meta":{"source":"tool"}}}
{"type":"message","id":"f0e0356d","parentId":"3fa018fa","timestamp":"2026-10-01T20:09:31.013Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call_7654","name":"read_file","input":{"file_path":"/tmp/proj-cc/retry.cfg"}}],"meta":{"source":"model"}}}
{"type":"message","id":"323f220d","parentId":"f0e0356d","timestamp":"2026-10-01T20:09:34.930Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call_bbb6","name":"edit_file","input":{"file_path":"/tmp/proj-cc/retry.cfg","old_string":"retry = 3","new_string":"retry = 5"}}],"meta":{"source":"model"}}}
{"type":"message","id":"5dbfba1f","parentId":"323f220d","timestamp":"2026-10-01T20:09:38.707Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call_da97","name":"write_file","input":{"file_path":"/tmp/proj-cc/backoff.txt","content":"backoff = exponential with jitter\n"}}],"meta":{"source":"model"}}}
{"type":"message","id":"e07b0c50","parentId":"58a914d2","timestamp":"2026-10-01T20:09:42.458Z","message":{"role":"assistant","content":[{"type":"text","text":"Added backoff.txt with an exponential backoff note."}],"meta":{"source":"model"}}}
`

func TestCommandCodeReadsTheV3Transcript(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COMMANDCODE_ROOT", root)
	dir := filepath.Join(root, "private-tmp-proj-cc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "1583a430-5538-475d-9190-0d726c7a374f.jsonl")
	if err := os.WriteFile(path, []byte(commandCodeV3), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCommandCodeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want the v3 transcript read", len(ss))
	}
	s := ss[0]
	if s.ID != "1583a430-5538-475d-9190-0d726c7a374f" {
		t.Errorf("id = %q", s.ID)
	}
	if want := "tmp/proj-cc"; s.Project != want {
		t.Errorf("project = %q, want %q from the header's cwd", s.Project, want)
	}
	byRole := map[string][]string{}
	for _, m := range s.Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
		if m.Time.IsZero() {
			t.Errorf("%s record has no time", m.Role)
		}
	}
	has := func(role, sub string) {
		t.Helper()
		for _, txt := range byRole[role] {
			if strings.Contains(txt, sub) {
				return
			}
		}
		t.Errorf("no %s record holding %q; got %q", role, sub, byRole[role])
	}
	has("user", "fix the retry loop")
	has("assistant", "exponential backoff")
	has(RoleToolOutput, "retry = 3")
	has(RoleCommand, "$ git status --short && cat retry.cfg")
	has(RoleFiles, "/tmp/proj-cc/retry.cfg")
	has(RoleFiles, "/tmp/proj-cc/backoff.txt")
	has(RoleEdit, "/tmp/proj-cc/retry.cfg\nretry = 3")
	has(RoleWrote, "/tmp/proj-cc/backoff.txt")

	// An appended turn read from an offset keeps the session the header names.
	more := `{"type":"message","id":"x1","parentId":"e07b0c50","timestamp":"2026-10-01T20:18:14.182Z","message":{"role":"user","content":[{"type":"text","text":"what file did you add"}]}}` + "\n"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(more); err != nil {
		t.Fatal(err)
	}
	f.Close()
	ss, err = ParseCommandCodeFileFromOffset(path, int64(len(commandCodeV3)))
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != s.ID || ss[0].Project != s.Project {
		t.Fatalf("appended turn = %+v, want it under the header's id and project", ss)
	}
}

// The client's own filter: a migration backup and the prompt history beside a
// transcript are not conversations. The .v2.bak is the old copy of a session
// the .jsonl now holds, so reading it would count the session twice.
func TestCommandCodeSkipsBackupAndPromptFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COMMANDCODE_ROOT", root)
	dir := filepath.Join(root, "private-tmp-proj-cc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.jsonl", "a.checkpoints.jsonl", "a.v2.bak", "a.prompts.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(commandCodeV3), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files := CommandCodeSessionFiles()
	if len(files) != 1 || filepath.Base(files[0]) != "a.jsonl" {
		t.Fatalf("files = %v, want a.jsonl alone", files)
	}
	if CommandCodeUnderRoot(filepath.Join(dir, "a.prompts.jsonl")) {
		t.Error("the prompt history was claimed as a transcript")
	}
	// doctor counts these as a deliberate skip, not as files it failed to read.
	if err := os.WriteFile(filepath.Join(dir, "a.meta.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if beside := CommandCodeCheckpointFiles(); len(beside) != 3 {
		t.Errorf("beside = %v, want checkpoints, prompts and meta", beside)
	}
}
