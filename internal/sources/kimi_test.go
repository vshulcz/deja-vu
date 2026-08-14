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
	if s.Harness != "kimi" || s.ID != "session_t01" || s.Project != "proj" || s.Title != "t" {
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
	_, wire := kimiFixture(t)
	torn := kimiWireHead + `{"type":"context.append_message","message":{"role":"user","co`
	if err := os.WriteFile(wire, []byte(torn), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseKimiFile(wire)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("torn tail: err=%v sessions=%d", err, len(ss))
	}
}

// kimiWireTools is one step that works: it speaks, runs a command, reads the
// file, makes an edit that fails, writes a note, reads an image, touches its
// todo list, and closes with more text. Shapes are the ones the Kimi CLI writes: args key
// `path` (not `file_path`), `command` for Bash, and results under
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
	wantOut := []string{"FAIL: TestX (0.01s)", "package main", "old_string not found"}
	if got := byRole[RoleToolOutput]; strings.Join(got, "|") != strings.Join(wantOut, "|") {
		t.Errorf("tool output = %q, want the failure, the file and the error — never the empty result", got)
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
