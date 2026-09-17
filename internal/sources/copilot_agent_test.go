package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCopilotAgentTranscript writes one of the extension's own transcripts,
// in the layout a newer Copilot Chat uses (#3637).
func writeCopilotAgentTranscript(t *testing.T, root, hash, id, workspace string, lines []string) string {
	t.Helper()
	dir := filepath.Join(root, "workspaceStorage", hash, "GitHub.copilot-chat", "transcripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if workspace != "" {
		wp := filepath.Join(root, "workspaceStorage", hash, "workspace.json")
		if err := os.WriteFile(wp, []byte(workspace), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// On VS Code Server 1.137 there is no `chatSessions` directory at all and the
// history is in the extension's own transcripts, so the source found zero
// files; on desktop 1.136.1 both layouts sit side by side (#3637).
func TestCopilotAgentTranscriptIsReadAndSearchable(t *testing.T) {
	root := copilotChatRoot(t)
	p := writeCopilotAgentTranscript(t, root, "h1", "s-agent", `{"folder":"file:///work/projA"}`, []string{
		`{"type":"session.start","data":{"sessionId":"s-agent","version":1,"producer":"copilot-agent","copilotVersion":"0.62.0","vscodeVersion":"1.137.0","startTime":"2026-09-14T05:46:01.086Z"},"id":"e1","timestamp":"2026-09-14T05:46:01.086Z","parentId":null}`,
		`{"type":"user.message","data":{"content":"why does the packer manifest fail"},"id":"e2","timestamp":"2026-09-14T05:46:10.000Z","parentId":"e1"}`,
		`{"type":"assistant.turn_start","data":{},"id":"e3","timestamp":"2026-09-14T05:46:11.000Z"}`,
		`{"type":"assistant.message","data":{"messageId":"m1","content":"The manifest names a source that is not built.","toolRequests":[{"toolCallId":"c1","name":"read_file","arguments":"{\"filePath\":\"/work/projA/packer/manifest.json\"}","type":"function"}]},"id":"e4","timestamp":"2026-09-14T05:46:12.000Z"}`,
		`{"type":"tool.execution_start","data":{"toolCallId":"c2","toolName":"run_in_terminal","arguments":{"command":"packer build -only=amd64 ."}},"id":"e5","timestamp":"2026-09-14T05:46:13.000Z"}`,
		`{"type":"tool.execution_complete","data":{"toolCallId":"c2","success":true},"id":"e6","timestamp":"2026-09-14T05:46:14.000Z"}`,
		`{"type":"assistant.turn_end","data":{},"id":"e7","timestamp":"2026-09-14T05:46:15.000Z"}`,
	})
	if KindForPath(p) != "copilot-chat" {
		t.Fatalf("KindForPath = %q, want copilot-chat", KindForPath(p))
	}
	files := CopilotChatSessionFiles()
	if len(files) != 1 || files[0] != p {
		t.Fatalf("discovery = %v", files)
	}
	ss := LoadCopilotChat()
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	s := ss[0]
	if s.ID != "s-agent" {
		t.Errorf("id = %q", s.ID)
	}
	if s.Project != "projA" {
		t.Errorf("project = %q, want the workspace.json folder", s.Project)
	}
	if s.Started.IsZero() {
		t.Error("session.start carried a start time and it was dropped")
	}
	var roles []string
	var joined string
	for _, m := range s.Messages {
		roles = append(roles, m.Role)
		joined += m.Role + ": " + m.Text + "\n"
	}
	for _, want := range []string{"why does the packer manifest fail", "names a source that is not built"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%q is missing from the session:\n%s", want, joined)
		}
	}
	if !strings.Contains(joined, "$ packer build -only=amd64 .") {
		t.Errorf("the command a tool call ran was not recorded as one:\n%s", joined)
	}
	if !strings.Contains(joined, "/work/projA/packer/manifest.json") {
		t.Errorf("the file a tool call touched was not recorded:\n%s", joined)
	}
	if strings.Count(joined, "user:") != 1 {
		t.Errorf("roles = %v", roles)
	}
}

// 11 of the reporter's 47 transcripts and all five on this machine hold no
// `user.message` at all: those are agent runs, and what the agent said and the
// files it touched are the only record of them.
func TestCopilotAgentTranscriptWithNoUserTurnIsStillIndexed(t *testing.T) {
	root := copilotChatRoot(t)
	writeCopilotAgentTranscript(t, root, "h2", "s-quiet", "", []string{
		`{"type":"session.start","data":{"sessionId":"s-quiet","startTime":"2026-09-06T15:14:41.310Z"},"id":"e1","timestamp":"2026-09-06T15:14:41.310Z"}`,
		`{"type":"assistant.message","data":{"messageId":"m1","content":"I will locate the manifest and check the validation failure.","toolRequests":[]},"id":"e2","timestamp":"2026-09-06T15:14:42.000Z"}`,
		`{"type":"tool.execution_start","data":{"toolCallId":"c1","toolName":"read_file","arguments":{"filePath":"/work/projB/main.go"}},"id":"e3","timestamp":"2026-09-06T15:14:43.000Z"}`,
	})
	ss := LoadCopilotChat()
	if len(ss) != 1 {
		t.Fatalf("an agent-only transcript was dropped: %d sessions", len(ss))
	}
	if ss[0].Title == "" {
		t.Error("a session with no user turn got no title")
	}
}

// `transcripts` is a generic directory name — this repository reads one for
// another harness — so the parent has to be the extension's own directory.
func TestAGenericTranscriptsDirectoryIsNotCopilotChats(t *testing.T) {
	root := copilotChatRoot(t)
	dir := filepath.Join(root, "workspaceStorage", "h3", "SomeOther.extension", "transcripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "x.jsonl")
	if err := os.WriteFile(p, []byte(`{"type":"user.message","data":{"content":"hello"},"timestamp":"2026-09-14T05:46:10.000Z"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if copilotAgentTranscript(p) {
		t.Error("a transcripts directory of somebody else's was claimed")
	}
	if KindForPath(p) == "copilot-chat" {
		t.Error("KindForPath claimed it")
	}
	if files := CopilotChatSessionFiles(); len(files) != 0 {
		t.Errorf("discovery took it: %v", files)
	}
}

// The file grows while the chat runs, so the last line of a live session is
// half-written. That is one skipped line, not a dropped session.
func TestCopilotAgentTranscriptSurvivesAHalfWrittenLastLine(t *testing.T) {
	root := copilotChatRoot(t)
	writeCopilotAgentTranscript(t, root, "h4", "s-live", "", []string{
		`{"type":"session.start","data":{"sessionId":"s-live","startTime":"2026-09-14T05:46:01.086Z"},"id":"e1","timestamp":"2026-09-14T05:46:01.086Z"}`,
		`{"type":"user.message","data":{"content":"the seeder dies with SHARDMAP_NOT_FOUND"},"id":"e2","timestamp":"2026-09-14T05:46:10.000Z"}`,
		`{"type":"assistant.message","data":{"messageId":"m1","content":"It needs SHARD_SEED_PROFILE`,
	})
	ss := LoadCopilotChat()
	if len(ss) != 1 {
		t.Fatalf("a live session was dropped: %d", len(ss))
	}
	if !strings.Contains(ss[0].Messages[0].Text, "SHARDMAP_NOT_FOUND") {
		t.Errorf("the whole lines before it were lost: %+v", ss[0].Messages)
	}
}
