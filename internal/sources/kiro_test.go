package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func jsonUnmarshalString(s string, v any) error { return json.Unmarshal([]byte(s), v) }

// A streamed reply is several records under one message id, each carrying the
// next piece. Read one per record and recall quotes a fragment, which is the
// thing worth pinning: the run has to come back as one answer (#3103).
func TestKiroCLIJoinsAStreamedReply(t *testing.T) {
	root := kiroFixtureRoot(t)
	ss, err := ParseKiroCLIFile(filepath.Join(root, "cli", "registry-kiro.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	if s.ID != "registry-kiro" {
		t.Errorf("id = %q, want the header's session_id", s.ID)
	}
	if !strings.Contains(s.Project, "api") {
		t.Errorf("project = %q, want it from the header's cwd", s.Project)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %d, want the prompt and one joined answer: %+v", len(s.Messages), s.Messages)
	}
	if s.Messages[0].Role != "user" || !strings.Contains(s.Messages[0].Text, "retry loop") {
		t.Errorf("first message = %+v, want the prompt", s.Messages[0])
	}
	answer := s.Messages[1]
	if answer.Role != "assistant" {
		t.Errorf("second message role = %q, want assistant", answer.Role)
	}
	if !strings.Contains(answer.Text, "needs <=") || !strings.Contains(answer.Text, "compares with <") {
		t.Errorf("the reply was not joined, so half the answer is missing: %q", answer.Text)
	}
	if answer.Time.IsZero() {
		t.Error("the answer has no time, so recency cannot rank it")
	}
}

// The IDE writes a different file in a different place, and its metadata half
// names the workspace.
func TestKiroIDEReadsTheWorkspaceSession(t *testing.T) {
	root := kiroFixtureRoot(t)
	path := filepath.Join(root, "w-api", "sess_00000000-0000-4000-8000-000000000000", "messages.jsonl")
	ss, err := ParseKiroIDEFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	if s.ID != "sess_00000000-0000-4000-8000-000000000000" {
		t.Errorf("id = %q, want the session.json id", s.ID)
	}
	if !strings.Contains(s.Project, "api") {
		t.Errorf("project = %q, want it from workspacePaths", s.Project)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %d, want the prompt and the answer: %+v", len(s.Messages), s.Messages)
	}
	if !strings.Contains(s.Messages[1].Text, "backfill") {
		t.Errorf("the answer is missing: %+v", s.Messages)
	}
	// The bookkeeping lines are not turns: a session_metadata or turn_end
	// record read as a message is an empty message in the middle of a recall.
	for _, m := range s.Messages {
		if m.Text == "" {
			t.Errorf("a bookkeeping record became a message: %+v", s.Messages)
		}
	}
}

// A kiro-cli --v3 TUI turn that ran a shell command, taken from a logged-in
// 2.22.0 stand: the prompt, the command, its output and the answer are read.
// The Reasoning records hold "..." in place of the encrypted thinking, and
// session_start holds the system prompt; neither is a turn.
func TestKiroV3ReadsAssistantAndToolRecords(t *testing.T) {
	path := filepath.Join("..", "..", "fixtures", "registry", "kiro-v3", "w-v3", "sess_00000000-0000-4000-8000-000000000003", "messages.jsonl")
	ss, err := ParseKiroIDEFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions = %d, %v", len(ss), err)
	}
	var got []string
	for _, m := range ss[0].Messages {
		got = append(got, m.Role+": "+strings.TrimSpace(m.Text))
	}
	// echo is below what deja keeps as a command (worthIndexing); the call
	// itself is read, checked below with a command that is kept.
	want := []string{
		"user: Run the shell command: echo deja-v3-stand  -- then reply with one short sentence saying what it printed.",
		RoleToolOutput + ": Output:\ndeja-v3-stand\n\n\nExit Code: 0",
		"assistant: It printed `deja-v3-stand`.",
	}
	if strings.Join(got, "\n|") != strings.Join(want, "\n|") {
		t.Errorf("messages:\n%s\nwant:\n%s", strings.Join(got, "\n|"), strings.Join(want, "\n|"))
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.Contains(line, `"type":"tool_call"`) {
			continue
		}
		line = strings.Replace(line, "echo deja-v3-stand", "go test ./pool/...", 1)
		var m map[string]any
		if err := jsonUnmarshalString(line, &m); err != nil {
			t.Fatal(err)
		}
		call, isCall, failed := kiroIDECall(m)
		work := kiroWorkRecords([]any{call}, nil, parseTimeAny(m["timestamp"]))
		if !isCall || failed || len(work) == 0 || work[0].Role != RoleCommand || work[0].Text != "$ go test ./pool/..." {
			t.Errorf("V3 tool_call = %v %v %v, want the command record", isCall, failed, work)
		}
		return
	}
	t.Error("fixture has no tool_call record")
}

// A record whose type is not a turn must not become one. The file carries the
// agent's own bookkeeping — context usage, turn boundaries, tool calls — and a
// line attributed to a role on the way past is a message with somebody else's
// words in it.
func TestKiroIDEDropsRecordsThatAreNotTurns(t *testing.T) {
	for _, line := range []string{
		`{"payload":{"type":"session_metadata","key":"contextUsage","content":"12.5"}}`,
		`{"payload":{"type":"turn_end","content":""}}`,
		`{"payload":{"type":"usage_summary","content":"elapsed 2500"}}`,
		`{"payload":{"type":"tool_call","content":"fs_read"}}`,
	} {
		var m map[string]any
		if err := jsonUnmarshalString(line, &m); err != nil {
			t.Fatal(err)
		}
		if role, text := kiroIDELine(m); role != "" || text != "" {
			t.Errorf("%s became a %q message %q", line, role, text)
		}
	}
}

// Older builds wrote the flat shape into the same file, so both are read.
func TestKiroIDEReadsTheFlatShape(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "w-api", "sess_old")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "messages.jsonl")
	lines := `{"timestamp":"2026-08-01T09:00:00Z","role":"user","content":"why is the index rebuilt on every start"}
{"timestamp":"2026-08-01T09:00:20Z","role":"assistant","content":"The version in the store was older than the binary's."}
`
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseKiroIDEFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("the flat shape was not read: %+v", ss)
	}
}

// Which reader gets a path is decided by where it sits, and the two layouts
// share a root.
func TestKiroClaimsItsOwnFilesOnly(t *testing.T) {
	root := kiroFixtureRoot(t)
	cli := filepath.Join(root, "cli", "registry-kiro.jsonl")
	ide := filepath.Join(root, "w-api", "sess_00000000-0000-4000-8000-000000000000", "messages.jsonl")
	if !KiroUnderCLI(cli) || KiroUnderIDE(cli) {
		t.Errorf("the CLI transcript was not claimed by the CLI reader alone: %s", cli)
	}
	if !KiroUnderIDE(ide) || KiroUnderCLI(ide) {
		t.Errorf("the IDE transcript was not claimed by the IDE reader alone: %s", ide)
	}
	if KiroUnderIDE(filepath.Join(root, "w-api", "notes", "messages.jsonl")) {
		t.Error("a messages.jsonl outside a sess_ directory was claimed as a session")
	}
	files := KiroSessionFiles()
	if len(files) != 2 {
		t.Errorf("files = %v, want both transcripts and neither header", files)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "session.json") {
			t.Errorf("the metadata half was listed as a transcript: %s", f)
		}
	}
}

// kiroFixtureRoot points the reader at the committed fixture store.
func kiroFixtureRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "registry", "kiro"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_KIRO_ROOT", root)
	t.Setenv("DEJA_KIRO_DB", filepath.Join(t.TempDir(), "data.sqlite3"))
	return root
}
