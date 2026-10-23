package sources

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cursor-agent writes a subagent's run to agent-transcripts/<parent>/subagents/
// <child>.jsonl, and the lines carry no ids, so the path is the only link. The
// child was skipped by default and, under DEJA_INCLUDE_SUBAGENTS=1, read as a
// session of its own with no parent: blame could not name the subagent that
// wrote a file, and show could not hang it under the run that spawned it.
func TestACursorSubagentNamesItsParentAndKeepsWhatItChanged(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CURSOR_CLI_ROOT", root)
	parent := "11111111-aaaa-4bbb-8ccc-000000000001"
	dir := filepath.Join(root, "projects", "Users-x-app", "agent-transcripts", parent, "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString(`{"role":"user","message":{"content":[{"type":"text","text":"Write the release notes into notes.txt"}]}}` + "\n")
	b.WriteString(`{"role":"assistant","message":{"content":[{"type":"text","text":"Writing it."},{"type":"tool_use","name":"Write","input":{"path":"/x/app/notes.txt","contents":"zebrafinch release notes, line one of them\n"}}]}}` + "\n")
	// Enough reading after the write that the tail cut has a middle to drop.
	for i := range 6 {
		fmt.Fprintf(&b, `{"role":"assistant","message":{"content":[{"type":"text","text":"checking step %d"}]}}`+"\n", i)
	}
	b.WriteString(`{"role":"assistant","message":{"content":[{"type":"text","text":"Done: notes.txt holds the notes."}]}}` + "\n")
	path := filepath.Join(dir, "22222222-aaaa-4bbb-8ccc-000000000002.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := CursorTranscripts(); len(got) != 1 || got[0] != path {
		t.Fatalf("the subagent transcript is not read by default: %v", got)
	}
	ss, err := ParseCursorTranscript(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %v", ss, err)
	}
	s := ss[0]
	if s.Kind != "subagent" || s.Parent != parent {
		t.Fatalf("kind %q parent %q, want subagent of %s", s.Kind, s.Parent, parent)
	}
	var wrote, middle bool
	for _, m := range s.Messages {
		wrote = wrote || m.Role == RoleWrote && strings.HasPrefix(m.Text, "/x/app/notes.txt")
		middle = middle || strings.Contains(m.Text, "checking step 1")
	}
	if !wrote {
		t.Errorf("the subagent's write is gone, so blame cannot name it: %+v", s.Messages)
	}
	if middle {
		t.Errorf("the middle of the run is kept by default: %+v", s.Messages)
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	ss, _ = ParseCursorTranscript(path)
	if len(ss) != 1 || ss[0].Parent != parent || len(ss[0].Messages) <= len(s.Messages) {
		t.Fatalf("DEJA_INCLUDE_SUBAGENTS=1 should take the whole run under its parent: %+v", ss)
	}

	// The primary transcript is nobody's child.
	top := filepath.Join(root, "projects", "Users-x-app", "agent-transcripts", parent, parent+".jsonl")
	if err := os.WriteFile(top, []byte(`{"role":"user","message":{"content":[{"type":"text","text":"hi"}]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ss, _ := ParseCursorTranscript(top); len(ss) != 1 || ss[0].Kind != "" || ss[0].Parent != "" {
		t.Errorf("primary transcript read as a subagent: %+v", ss)
	}
}
