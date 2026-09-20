package sources

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A commit that only adds lines has no replaced side, so what a session wrote
// is the only evidence those lines were ever in one. Three readers counted
// their replace tool as an edit and their write tool as a path only, so a file
// created wholesale left nothing to attribute from (#595, #3810).
func TestWholeFileWritesAreRecordedAsWritten(t *testing.T) {
	const line = "// Acquire waits for a free connection or the context's deadline."
	const second = "func (p *Pool) Acquire(ctx context.Context) (*Conn, error) {"
	h, ok := HashWrittenLine(line)
	if !ok {
		t.Fatal("the fixture line is too short to be evidence")
	}
	h2, ok := HashWrittenLine(second)
	if !ok {
		t.Fatal("the fixture line is too short to be evidence")
	}
	want := strconv.FormatUint(h, 16) + " " + strconv.FormatUint(h2, 16)

	t.Run("copilot creates a file", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "session-state", "s1")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"session.start","sessionId":"s1","timestamp":"2026-09-20T09:00:00Z","cwd":"/w/app"}
{"type":"tool.execution_start","timestamp":"2026-09-20T09:00:01Z","data":{"toolName":"create","arguments":{"path":"/w/app/internal/pool/pool.go","file_text":"` + line + `\n` + second + `\n"}}}
`
		p := filepath.Join(dir, "events.jsonl")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		ss, err := ParseCopilotFile(p)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%v %#v", err, ss)
		}
		assertWroteHashes(t, ss[0].Messages, "/w/app/internal/pool/pool.go", want)
	})

	t.Run("cursor writes a file", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "projects", "Users-me-app", "agent-transcripts", "s-write")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"role":"user","message":{"content":[{"type":"text","text":"add the pool"}]}}
{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Write","input":{"path":"/w/app/internal/pool/pool.go","contents":"` + line + `\n` + second + `\n"}}]}}
`
		p := filepath.Join(dir, "s-write.jsonl")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		ss, err := ParseCursorTranscript(p)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%v %#v", err, ss)
		}
		assertWroteHashes(t, ss[0].Messages, "/w/app/internal/pool/pool.go", want)
	})

	t.Run("kimi writes a file", func(t *testing.T) {
		_, wire := kimiFixture(t)
		body := kimiWireHead + `{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"t1","name":"Write","args":{"path":"/w/proj/internal/pool/pool.go","content":"` + line + `\n` + second + `\n"}},"time":1782295203900}
`
		if err := os.WriteFile(wire, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		ss, err := ParseKimiFile(wire)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%v %#v", err, ss)
		}
		assertWroteHashes(t, ss[0].Messages, "/w/proj/internal/pool/pool.go", want)
	})
}

// assertWroteHashes pins the record to exactly the lines of the fixture: a
// record holding anything else would attribute a line the session never wrote.
func assertWroteHashes(t *testing.T, msgs []model.Message, path, want string) {
	t.Helper()
	var wrote []string
	for _, m := range msgs {
		if m.Role == RoleWrote {
			wrote = append(wrote, m.Text)
		}
	}
	if len(wrote) != 1 {
		t.Fatalf("wrote = %q, want one record", wrote)
	}
	got, rest, ok := strings.Cut(wrote[0], "\n")
	if !ok || got != path {
		t.Fatalf("the record is filed under %q", got)
	}
	if strings.Join(strings.Fields(rest), " ") != want {
		t.Fatalf("hashes = %q, want the written lines %q", rest, want)
	}
}
