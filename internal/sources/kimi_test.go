package sources

import (
	"os"
	"path/filepath"
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
