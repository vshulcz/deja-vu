package sources

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

const (
	copilotParentID = "f67048ad-042c-42f4-ac33-5db51c4d48b0"
	copilotChildID  = "717532a2-bf69-4164-be09-03daacd1762f"
	copilotForkID   = "2f07ff7f-aa0e-4b85-93fb-2096b6e9833f"
)

// The shape Copilot CLI 1.0.98 writes for a `task` call: the sub-agent's
// records sit in the parent's events.jsonl, each one stamped with agentId.
func copilotSubagentLines(sessionID, start string) []string {
	return []string{
		`{"type":"session.start","data":{"sessionId":"` + sessionID + `","copilotVersion":"1.0.98","startTime":"` + start + `","context":{"cwd":"/Users/x/work/my-app"}},"timestamp":"` + start + `","parentId":null}`,
		`{"type":"user.message","data":{"content":"write the release notes with a helper agent"},"timestamp":"2026-10-18T05:31:47.305Z"}`,
		`{"type":"tool.execution_start","data":{"toolCallId":"call_task","toolName":"task","arguments":{"description":"write notes","prompt":"draft the release notes file","agent_type":"general-purpose","name":"scribe"}},"timestamp":"2026-10-18T05:31:47.355Z"}`,
		`{"type":"subagent.started","agentId":"` + copilotChildID + `","data":{"toolCallId":"call_task","agentName":"general-purpose","agentDisplayName":"scribe","agentType":"general-purpose","resumable":false},"timestamp":"2026-10-18T05:31:47.356Z"}`,
		`{"type":"user.message","agentId":"` + copilotChildID + `","data":{"content":"draft the release notes file"},"timestamp":"2026-10-18T05:31:47.399Z"}`,
		`{"type":"tool.execution_start","agentId":"` + copilotChildID + `","data":{"toolCallId":"call_create","toolName":"create","arguments":{"path":"/Users/x/work/my-app/notes.txt","file_text":"release notes drafted by the helper sub-agent\n"},"parentToolCallId":"call_task"},"timestamp":"2026-10-18T05:31:47.407Z"}`,
		`{"type":"tool.execution_complete","agentId":"` + copilotChildID + `","data":{"toolCallId":"call_create","success":true,"result":{"content":"Created file notes.txt"}},"timestamp":"2026-10-18T05:31:47.426Z"}`,
		`{"type":"assistant.message","agentId":"` + copilotChildID + `","data":{"content":"Created notes.txt with the draft.","parentToolCallId":"call_task"},"timestamp":"2026-10-18T05:31:49.063Z"}`,
		`{"type":"subagent.completed","agentId":"` + copilotChildID + `","data":{"toolCallId":"call_task","agentName":"general-purpose"},"timestamp":"2026-10-18T05:31:49.064Z"}`,
		`{"type":"tool.execution_complete","data":{"toolCallId":"call_task","success":true,"result":{"content":"Created notes.txt with the draft."}},"timestamp":"2026-10-18T05:31:49.067Z"}`,
		`{"type":"assistant.message","data":{"content":"The helper wrote notes.txt."},"timestamp":"2026-10-18T05:31:51.660Z"}`,
		`{"type":"user.message","data":{"content":"the parent writes its own file"},"timestamp":"2026-10-18T05:33:10.000Z"}`,
		`{"type":"tool.execution_start","data":{"toolCallId":"call_parent","toolName":"create","arguments":{"path":"/Users/x/work/my-app/parent.txt","file_text":"the parent session wrote this whole line itself\n"}},"timestamp":"2026-10-18T05:33:10.100Z"}`,
	}
}

func copilotByID(ss []model.Session) map[string]model.Session {
	out := map[string]model.Session{}
	for _, s := range ss {
		out[s.ID] = s
	}
	return out
}

func copilotHasText(s model.Session, role, sub string) bool {
	for _, m := range s.Messages {
		if m.Role == role && strings.Contains(m.Text, sub) {
			return true
		}
	}
	return false
}

// A sub-agent's records were folded into the parent: the prompt the parent
// agent wrote for it read as the person's turn, and its edits as the parent's
// (#5216).
func TestCopilotSubagentIsItsOwnSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	p := writeCopilotFixture(t, root, copilotParentID, copilotSubagentLines(copilotParentID, "2026-10-18T05:31:46.707Z"))
	ss, err := ParseCopilotFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 2 || ss[0].ID != copilotParentID {
		t.Fatalf("sessions = %d (first %q), want the parent then its sub-agent", len(ss), ss[0].ID)
	}
	by := copilotByID(ss)
	parent, child := by[copilotParentID], by[copilotChildID]
	if parent.Kind != "" || parent.Parent != "" {
		t.Errorf("parent kind/parent = %q/%q", parent.Kind, parent.Parent)
	}
	if copilotHasText(parent, "user", "draft the release notes file") {
		t.Errorf("the sub-agent's task prompt reads as the person's turn in the parent")
	}
	if copilotHasText(parent, RoleWrote, "notes.txt") {
		t.Errorf("the sub-agent's write is filed under the parent")
	}
	if !copilotHasText(parent, RoleWrote, "parent.txt") || !copilotHasText(parent, "user", "write the release notes") {
		t.Errorf("parent lost its own records: %#v", parent.Messages)
	}
	if child.Kind != "sidechain" || child.Parent != copilotParentID || child.Agent != "general-purpose" {
		t.Errorf("child kind/parent/agent = %q/%q/%q", child.Kind, child.Parent, child.Agent)
	}
	if child.Harness != "copilot" || child.Project != parent.Project || child.Path != p {
		t.Errorf("child harness/project/path = %q/%q/%q", child.Harness, child.Project, child.Path)
	}
	if !copilotHasText(child, "user", "draft the release notes file") || !copilotHasText(child, RoleWrote, "notes.txt") ||
		!copilotHasText(child, "assistant", "Created notes.txt") {
		t.Errorf("child records = %#v", child.Messages)
	}

	// A tail that closes a run goes back for a whole read, which cuts it the
	// way a rebuild does; one inside the run, or past it, is read on its own.
	lines := copilotSubagentLines(copilotParentID, "2026-10-18T05:31:46.707Z")
	if off := int64(len(strings.Join(lines[:5], "\n") + "\n")); copilotResumes(p, off) {
		t.Errorf("a tail holding subagent.completed is read on its own")
	}
	if off := int64(len(strings.Join(lines[:6], "\n") + "\n")); copilotResumes(p, off) {
		t.Errorf("a tail holding subagent.completed is read on its own")
	}
	if off := int64(len(strings.Join(lines[:9], "\n") + "\n")); !copilotResumes(p, off) {
		t.Errorf("a tail past the run is read whole")
	}
	mid := writeCopilotFixture(t, root, "mid", lines[:7])
	if off := int64(len(strings.Join(lines[:5], "\n") + "\n")); !copilotResumes(mid, off) {
		t.Errorf("a tail in the middle of a run is read whole")
	}
	// A run cut off with no subagent.completed heals when the session ends.
	ended := writeCopilotFixture(t, root, "ended", append(append([]string{}, lines[:7]...), `{"type":"session.shutdown","data":{},"timestamp":"2026-10-18T05:40:00.000Z"}`))
	if off := int64(len(strings.Join(lines[:5], "\n") + "\n")); copilotResumes(ended, off) {
		t.Errorf("a tail holding session.shutdown is read on its own")
	}
	if ss, _ := ParseCopilotFileFromOffset(mid, int64(len(strings.Join(lines[:5], "\n")+"\n"))); copilotByID(ss)[copilotChildID].Parent != copilotParentID {
		t.Errorf("a tail in the middle of a run lost its parent: %#v", ss)
	}

	// DEJA_INCLUDE_SUBAGENTS=0 leaves the run out, and still not in the parent.
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	off, _ := ParseCopilotFile(p)
	if len(off) != 1 || copilotHasText(off[0], "user", "draft the release notes file") {
		t.Errorf("with =0: %d sessions, parent %#v", len(off), off[0].Messages)
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")

	// The hooks read the head of the file as the parent's, child records in it
	// or not.
	head, err := CopilotSessionHead(copilotParentID)
	if err != nil || head.ID != copilotParentID || head.Kind != "" {
		t.Errorf("head = %q %q, %v", head.ID, head.Kind, err)
	}
}

// /fork copies the source's records with their times, the sub-agent's among
// them, then says where it came from in a session.info record. The fork named
// no source, and its copy of the sub-agent claimed the same id as the original
// (#5217).
func TestCopilotForkNamesItsSource(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	lines := copilotSubagentLines(copilotForkID, "2026-10-18T05:34:00.000Z")
	lines = append(lines,
		`{"type":"session.info","data":{"infoType":"fork","message":"Forked from \"write the release notes (f00dfeed-0000-4000-8000-000000000000) here\" (`+copilotParentID+`) as \"write the release notes (fork)\" (`+copilotForkID+`)."},"timestamp":"2026-10-18T05:34:00.002Z"}`,
		`{"type":"user.message","data":{"content":"hi from the fork"},"timestamp":"2026-10-18T05:34:10.000Z"}`,
		`{"type":"subagent.started","agentId":"9e0b1c2d-0000-4000-8000-00000000f0f0","data":{"toolCallId":"call_t2","agentName":"explore"},"timestamp":"2026-10-18T05:34:11.000Z"}`,
		`{"type":"assistant.message","agentId":"9e0b1c2d-0000-4000-8000-00000000f0f0","data":{"content":"the fork's own helper answered"},"timestamp":"2026-10-18T05:34:12.000Z"}`,
	)
	p := writeCopilotFixture(t, root, copilotForkID, lines)
	ss, err := ParseCopilotFile(p)
	if err != nil {
		t.Fatal(err)
	}
	by := copilotByID(ss)
	fork := by[copilotForkID]
	if fork.Kind != "fork" || fork.Parent != copilotParentID {
		t.Errorf("fork kind/parent = %q/%q, want fork/%s", fork.Kind, fork.Parent, copilotParentID)
	}
	if _, ok := by[copilotChildID]; ok {
		t.Errorf("the fork's copy of the source's sub-agent is read as a session under the original's id")
	}
	if own := by["9e0b1c2d-0000-4000-8000-00000000f0f0"]; own.Parent != copilotForkID || own.Kind != "sidechain" || own.Agent != "explore" {
		t.Errorf("the fork's own sub-agent = %q/%q/%q", own.Kind, own.Parent, own.Agent)
	}
	if !copilotHasText(fork, "user", "hi from the fork") || !copilotHasText(fork, RoleWrote, "parent.txt") {
		t.Errorf("fork records = %#v", fork.Messages)
	}
	if len(ss) != 2 {
		t.Errorf("sessions = %d, want the fork and its own sub-agent", len(ss))
	}

	// Read before its note is written, the fork's copy of the run is still
	// not a session: its records predate the fork's start.
	early := writeCopilotFixture(t, root, "early", copilotSubagentLines(copilotForkID, "2026-10-18T05:34:00.000Z"))
	es, _ := ParseCopilotFile(early)
	if _, ok := copilotByID(es)[copilotChildID]; ok || len(es) != 1 {
		t.Errorf("a fork read before its note: %d sessions, copied run kept = %v", len(es), ok)
	}

	// Without startTime the record's own time is the fork's start.
	var bare []string
	for _, l := range copilotSubagentLines(copilotForkID, "2026-10-18T05:34:00.000Z") {
		bare = append(bare, strings.Replace(l, `"startTime":"2026-10-18T05:34:00.000Z",`, "", 1))
	}
	bs, _ := ParseCopilotFile(writeCopilotFixture(t, root, "bare", bare))
	if _, ok := copilotByID(bs)[copilotChildID]; ok {
		t.Errorf("with no startTime, a fork read before its note keeps the copied run")
	}

	// The source's own note about the fork names the fork, not a source.
	src := copilotSubagentLines(copilotParentID, "2026-10-18T05:31:46.707Z")
	src = append(src, `{"type":"session.info","data":{"infoType":"fork","message":"Forked this session into \"write the release notes (fork)\" (`+copilotForkID+`)."},"timestamp":"2026-10-18T05:34:00.004Z"}`)
	ps, _ := ParseCopilotFile(writeCopilotFixture(t, root, copilotParentID, src))
	if s := copilotByID(ps)[copilotParentID]; s.Kind != "" || s.Parent != "" {
		t.Errorf("source read as a fork: %q/%q", s.Kind, s.Parent)
	}
}

// By default a long run is cut the way a Claude Code sub-agent file is: the
// task, the last turns and what it changed (#5216).
func TestCopilotSubagentKeepsItsTaskTailAndEdits(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	lines := copilotSubagentLines(copilotParentID, "2026-10-18T05:31:46.707Z")
	run := lines[:8]
	for i := 0; i < 6; i++ {
		run = append(run, `{"type":"assistant.message","agentId":"`+copilotChildID+`","data":{"content":"reading more of the repo, step `+string(rune('a'+i))+`"},"timestamp":"2026-10-18T05:31:50.00`+string(rune('0'+i))+`Z"}`)
	}
	p := writeCopilotFixture(t, root, copilotParentID, append(run, lines[8:]...))
	for _, tc := range []struct {
		include string
		whole   bool
	}{{"", false}, {"1", true}} {
		t.Setenv("DEJA_INCLUDE_SUBAGENTS", tc.include)
		ss, _ := ParseCopilotFile(p)
		c := copilotByID(ss)[copilotChildID]
		if !copilotHasText(c, "user", "draft the release notes file") || !copilotHasText(c, RoleWrote, "notes.txt") {
			t.Errorf("include=%q: task or write lost: %#v", tc.include, c.Messages)
		}
		if got := copilotHasText(c, "assistant", "step a"); got != tc.whole {
			t.Errorf("include=%q: middle turn kept = %v, want %v", tc.include, got, tc.whole)
		}
	}
}
