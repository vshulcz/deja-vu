package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func kimiFixture(t *testing.T) (root, wire string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("KIMI_CODE_HOME", "")
	t.Setenv("DEJA_KIMI_ROOT", filepath.Join(root, "kimi"))
	dir := filepath.Join(root, "kimi", "sessions", "wd_demo_ab", "session_t01", "agents", "main")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := `{"createdAt":"2026-07-01T10:00:00.000Z","updatedAt":"2026-07-01T10:00:05.000Z","title":"t","workDir":"/home/u/work/proj"}`
	if err := os.WriteFile(filepath.Join(root, "kimi", "sessions", "wd_demo_ab", "session_t01", "state.json"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(dir, "wire.jsonl")
}

const kimiWireHead = `{"type":"metadata","protocol_version":"1.4","created_at":1782295200000}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"first question"}]},"time":1782295201000}
{"type":"context.append_loop_event","event":{"type":"step.begin","uuid":"s1"},"time":1782295201100}
{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"think","text":"hidden"}},"time":1782295201150}
{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"answer one, "}},"time":1782295201200}
{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"joined"}},"time":1782295201300}
{"type":"context.append_loop_event","event":{"type":"step.end","uuid":"s1","finishReason":"end_turn"},"time":1782295201400}
`

func TestParseKimiReconstructsStreamedAssistant(t *testing.T) {
	_, wire := kimiFixture(t)
	if err := os.WriteFile(wire, []byte(kimiWireHead), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseKimiFile(wire)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	s := ss[0]
	if s.Harness != "kimi" || s.ID != "session_t01" || s.Project != "work/proj" || s.Title != "t" {
		t.Fatalf("meta: %+v", s)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %d: %+v", len(s.Messages), s.Messages)
	}
	if s.Messages[0].Role != "user" || s.Messages[0].Text != "first question" {
		t.Fatalf("user msg: %+v", s.Messages[0])
	}
	if s.Messages[1].Role != "assistant" || s.Messages[1].Text != "answer one, joined" {
		t.Fatalf("assistant msg: %+v", s.Messages[1])
	}
	for _, m := range s.Messages {
		if m.Text == "hidden" {
			t.Fatal("think part leaked into the index")
		}
	}
}

func TestParseKimiMidStreamAndIncrementalAppend(t *testing.T) {
	_, wire := kimiFixture(t)
	// File ends mid-step: content.part written, no step.end yet.
	head := kimiWireHead + `{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"second question"}]},"time":1782295202000}
{"type":"context.append_loop_event","event":{"type":"step.begin","uuid":"s2"},"time":1782295202100}
{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"partial answer"}},"time":1782295202200}
`
	if err := os.WriteFile(wire, []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseKimiFile(wire)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v", err)
	}
	last := ss[0].Messages[len(ss[0].Messages)-1]
	if last.Role != "assistant" || last.Text != "partial answer" {
		t.Fatalf("mid-stream flush lost the response: %+v", ss[0].Messages)
	}
	// Append the rest and reparse from the previous offset.
	offset := int64(len(head))
	rest := `{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":" continues"}},"time":1782295202300}
{"type":"context.append_loop_event","event":{"type":"step.end","uuid":"s2","finishReason":"end_turn"},"time":1782295202400}
`
	if err := os.WriteFile(wire, []byte(head+rest), 0o644); err != nil {
		t.Fatal(err)
	}
	inc, err := ParseKimiFileFromOffset(wire, offset)
	if err != nil || len(inc) != 1 {
		t.Fatalf("incremental parse: %v %d", err, len(inc))
	}
	tail := inc[0].Messages[len(inc[0].Messages)-1]
	if tail.Role != "assistant" || tail.Text != "continues" {
		t.Fatalf("appended remainder: %+v", inc[0].Messages)
	}
}

func TestParseKimiToleratesTornTail(t *testing.T) {
	// A torn tail is what a crash mid-write leaves behind. The message tear is
	// the original case; tool.call and tool.result lines are the longer ones —
	// an args blob is where a write is most likely to be caught mid-flush — so
	// each shape is torn in turn. The guarantee is the same for all three: the
	// torn line vanishes, everything before it comes out untouched.
	_, wire := kimiFixture(t)
	if err := os.WriteFile(wire, []byte(kimiWireTools), 0o644); err != nil {
		t.Fatal(err)
	}
	whole, err := ParseKimiFile(wire)
	if err != nil || len(whole) != 1 {
		t.Fatalf("baseline: %v %d", err, len(whole))
	}
	tears := []struct{ name, tail string }{
		{"message", `{"type":"context.append_message","message":{"role":"user","co`},
		{"tool call", `{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"t9","name":"Edit","args":{"path":"/w/proj/main.go","old_string":"the old bytes, torn off mid-`},
		{"tool result", `{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"t9","result":{"output":"go: downloading github.com/`},
	}
	for _, tear := range tears {
		if err := os.WriteFile(wire, []byte(kimiWireTools+tear.tail), 0o644); err != nil {
			t.Fatal(err)
		}
		ss, err := ParseKimiFile(wire)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s tear: err=%v sessions=%d", tear.name, err, len(ss))
		}
		if got, want := len(ss[0].Messages), len(whole[0].Messages); got != want {
			t.Errorf("%s tear: messages = %d, want the %d the untorn file yields", tear.name, got, want)
		}
	}
}

// kimiWireTools is one step that works: it speaks, runs a command, reads the
// file, makes an edit that fails, writes a note, reads an image, touches its
// todo list — a call that yields no record, though its result still carries
// output — and closes with more text. Shapes are the ones the Kimi CLI writes:
// args key `path` (not `file_path`), `command` for Bash, and results under
// `result.output` — plain, isError, or empty.
const kimiWireTools = kimiWireHead + `{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"please fix the test"}]},"time":1782295203000}
{"type":"context.append_loop_event","event":{"type":"step.begin","uuid":"s2"},"time":1782295203100}
{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"looking at the failing test"}},"time":1782295203200}
{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"t1","name":"Bash","args":{"command":"go test ./...","timeout":60}},"time":1782295203300}
{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"t1","result":{"output":"FAIL: TestX (0.01s)"}},"time":1782295203400}
{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"t2","name":"Read","args":{"path":"/w/proj/main.go"}},"time":1782295203500}
{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"t2","result":{"output":"package main"}},"time":1782295203600}
{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"t3","name":"Edit","args":{"path":"/w/proj/main.go","old_string":"println(\"hello\")","new_string":"println(\"goodbye\")"}},"time":1782295203700}
{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"t3","result":{"isError":true,"output":"old_string not found"}},"time":1782295203800}
{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"t4","name":"Write","args":{"path":"/w/proj/notes.txt","content":"alpha"}},"time":1782295203900}
{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"t4","result":{"output":""}},"time":1782295204000}
{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"t5","name":"ReadMediaFile","args":{"path":"/w/proj/cover.png"}},"time":1782295204050}
{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"t6","name":"TodoList","args":{"items":[]}},"time":1782295204100}
{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"t6","result":{"output":"2 todos, both open"}},"time":1782295204150}
{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":" — fixed"}},"time":1782295204200}
{"type":"context.append_loop_event","event":{"type":"step.end","uuid":"s2","finishReason":"end_turn"},"time":1782295204300}
`

func TestParseKimiEmitsWorkRecords(t *testing.T) {
	_, wire := kimiFixture(t)
	if err := os.WriteFile(wire, []byte(kimiWireTools), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseKimiFile(wire)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	if got := byRole[RoleCommand]; len(got) != 1 || got[0] != "$ go test ./..." {
		t.Errorf("commands = %q, want the test run alone — TodoList and friends carry no command", got)
	}
	wantFiles := []string{"/w/proj/main.go", "/w/proj/main.go", "/w/proj/notes.txt", "/w/proj/cover.png"}
	if got := byRole[RoleFiles]; strings.Join(got, ",") != strings.Join(wantFiles, ",") {
		t.Errorf("files = %q, want the read, the edit, the write and the media read in order", got)
	}
	if got := byRole[RoleEdit]; len(got) != 1 || got[0] != "/w/proj/main.go\nprintln(\"hello\")" ||
		strings.Contains(strings.Join(got, ""), "goodbye") {
		t.Errorf("edit = %q, want only the bytes that stopped existing", got)
	}
	// The todo note lands although its call yields no record: tool.result is
	// indexed on its own switch, independent of what the call produced.
	wantOut := []string{"FAIL: TestX (0.01s)", "package main", "old_string not found", "2 todos, both open"}
	if got := byRole[RoleToolOutput]; strings.Join(got, "|") != strings.Join(wantOut, "|") {
		t.Errorf("tool output = %q, want the failure, the file, the error and the todo note — never the empty result", got)
	}
	// The stream flushes when a call yields a record, so the words that led
	// to the first command land before it, not at step.end.
	var order []string
	for _, m := range ss[0].Messages {
		if m.Role == "assistant" || m.Role == RoleCommand {
			order = append(order, m.Text)
		}
	}
	want := []string{"answer one, joined", "looking at the failing test", "$ go test ./...", "— fixed"}
	if strings.Join(order, "|") != strings.Join(want, "|") {
		t.Errorf("order = %q, want the speech ahead of the command it led to", order)
	}
}

func TestParseKimiToolRecordSwitchesOff(t *testing.T) {
	_, wire := kimiFixture(t)
	t.Setenv("DEJA_INDEX_COMMANDS", "0")
	t.Setenv("DEJA_INDEX_PATHS", "0")
	t.Setenv("DEJA_INDEX_EDITS", "0")
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	if err := os.WriteFile(wire, []byte(kimiWireTools), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseKimiFile(wire)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v", err)
	}
	// With every switch off, no record-driven flush fires either: the step's
	// text comes out as the single message it always was.
	last := ss[0].Messages[len(ss[0].Messages)-1]
	if last.Role != "assistant" || last.Text != "looking at the failing test — fixed" {
		t.Fatalf("switched off, want the untouched stream back: %+v", ss[0].Messages)
	}
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleCommand, RoleFiles, RoleEdit, RoleToolOutput:
			t.Fatalf("record %q survived its switch", m.Role)
		}
	}
}

func TestKimiSessionFilesSkipsSubAgents(t *testing.T) {
	root, wire := kimiFixture(t)
	if err := os.WriteFile(wire, []byte(kimiWireHead), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "kimi", "sessions", "wd_demo_ab", "session_t01", "agents", "agent-x1")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "wire.jsonl"), []byte(kimiWireHead), 0o644); err != nil {
		t.Fatal(err)
	}
	files := KimiSessionFiles()
	if len(files) != 1 || files[0] != wire {
		t.Fatalf("sub-agent wire indexed: %v", files)
	}
}

// Records as kimi-code 0.28.1 wrote them: a failed Bash call's result is
// flagged isError and ends with the tool's own footer (#4262).
const kimiWireExit = `{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]},"time":1790870004000}
{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"call_02ee5295c2c3","name":"Bash","args":{"command":"go vet ./nonexistent"}},"time":1790870004786}
{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"call_e0ddee76d756","name":"Bash","args":{"command":"go build ./..."}},"time":1790870004790}
{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"call_5","name":"Bash","args":{"command":"go test ./retry"}},"time":1790870004795}
{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"call_6","name":"Bash","args":{"command":"make release"}},"time":1790870004796}
{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"call_02ee5295c2c3","result":{"output":"go: cannot find main module, but found .git/config in /private/tmp/proj-kimi\n\tto create a module there, run:\n\tgo mod init\nCommand failed with exit code: 1.","isError":true}},"time":1790870004853}
{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"call_e0ddee76d756","result":{"output":"printing the footer it would write\nCommand failed with exit code: 2.\n"}},"time":1790870004900}
{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"call_5","result":{"output":"--- FAIL: TestRetry [...truncated]\nCommand failed with exit code: 3. Output is truncated to fit in the message.\n\n[Full output saved]\ntask_id: b1\noutput_path: /tmp/b1.log\noutput_size_bytes: 90000\nnext_step: Use Read with output_path to page through the full log.","isError":true}},"time":1790870004950}
{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"call_6","result":{"output":"building\nCommand killed by timeout (2m)","isError":true}},"time":1790870004960}
`

func TestKimiFailedCommandCarriesItsExitCode(t *testing.T) {
	_, wire := kimiFixture(t)
	if err := os.WriteFile(wire, []byte(kimiWireExit), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseKimiFile(wire)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	var cmds []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleCommand {
			cmds = append(cmds, m.Text)
		}
	}
	// The clean run ends on the footer itself but is not flagged isError,
	// and a timeout carries no exit code: both stay bare.
	want := []string{"$ go vet ./nonexistent  → exit 1", "$ go build ./...", "$ go test ./retry  → exit 3", "$ make release"}
	if strings.Join(cmds, "|") != strings.Join(want, "|") {
		t.Fatalf("commands = %q, want %q", cmds, want)
	}
}

// The Kimi Resumes hook is two rules, and either one sends the file back for
// a whole read: a failed result for a call stored already (#4443), and a reply
// streamed across the offset (#4445). Each case here is one only the other
// rule catches, so dropping either from kimiTailResumes fails it.
func TestKimiTailResumesKeepsBothRules(t *testing.T) {
	head := `{"type":"metadata","protocol_version":"1.4","created_at":1790870000000}` + "\n" +
		`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"fix it"}]},"time":1790870001000}` + "\n" +
		`{"type":"context.append_loop_event","event":{"type":"step.begin","uuid":"s1"},"time":1790870001100}` + "\n"
	for _, c := range []struct {
		name, before, after string
		want                bool
	}{
		{
			// The step ended before the offset, so the stream rule lets it
			// through; the failed result answers a call already stored.
			name:   "failed result after a closed step",
			before: `{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"c1","name":"Bash","args":{"command":"go vet"}},"time":1790870002000}` + "\n" + `{"type":"context.append_loop_event","event":{"type":"step.end","uuid":"s1"},"time":1790870002100}` + "\n",
			after:  `{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"c1","result":{"output":"Command failed with exit code: 2.","isError":true}},"time":1790870002200}` + "\n",
		},
		{
			// No tool at all, so the answering rule lets it through; the
			// reply's text goes on past the offset.
			name:   "reply streamed across the offset",
			before: `{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"half "}},"time":1790870002000}` + "\n",
			after:  `{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"done"}},"time":1790870002100}` + "\n",
		},
		{
			name:   "next turn after a finished step",
			before: `{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"done"}},"time":1790870002000}` + "\n" + `{"type":"context.append_loop_event","event":{"type":"step.end","uuid":"s1"},"time":1790870002100}` + "\n",
			after:  `{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"thanks"}]},"time":1790870003000}` + "\n",
			want:   true,
		},
		{
			// A call and its failure in the same tail: nothing stored
			// misses the exit.
			name:   "call and failure both in the tail",
			before: `{"type":"context.append_loop_event","event":{"type":"step.end","uuid":"s1"},"time":1790870002000}` + "\n",
			after:  `{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"c2","name":"Bash","args":{"command":"go vet"}},"time":1790870002100}` + "\n" + `{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"c2","result":{"output":"Command failed with exit code: 2.","isError":true}},"time":1790870002200}` + "\n",
			want:   true,
		},
		{
			// A tool call inside an open step counts as the stream going on,
			// and the reply that closes it in the tail is its end, not more
			// of it; a message line without a message changes nothing.
			name:   "open step closed by the reply",
			before: `{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"c3","name":"Read","args":{}},"time":1790870002000}` + "\n" + `{"type":"context.append_message"}` + "\n",
			after:  `{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"think","think":"hm"}},"time":1790870002100}` + "\n" + `{"type":"context.append_message","message":{"role":"assistant","content":[{"type":"text","text":"done"}]},"time":1790870002200}` + "\n" + `{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"late"}},"time":1790870002300}` + "\n",
			want:   true,
		},
		{
			name:   "tool result streamed across the offset",
			before: `{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"reading "}},"time":1790870002000}` + "\n",
			after:  `{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"c4","name":"Read","args":{}},"time":1790870002100}` + "\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "wire.jsonl")
			if err := os.WriteFile(path, []byte(head+c.before+c.after), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := kimiTailResumes(path, int64(len(head+c.before))); got != c.want {
				t.Fatalf("kimiTailResumes = %v, want %v", got, c.want)
			}
			if !kimiTailResumes(path, 0) {
				t.Fatal("a read from the start must resume")
			}
		})
	}
}
