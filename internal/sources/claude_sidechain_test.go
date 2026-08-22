package sources

import (
	"os"
	"path/filepath"
	"testing"
)

// Every line of a sidechain file repeats the parent's sessionId, so keying on
// it folded a subagent's whole run into the parent and one of the two won. The
// durable id is agentId (#1384). Fixture fields are the ones a real Claude
// Code 2.1.226 sidechain carries.
func TestClaudeSidechainIsItsOwnSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")

	parentID := "2915986c-9f79-4f59-aed1-6cb929ca62b8"
	dir := filepath.Join(root, "-work-app", parentID, "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "agent-a768d0f31f74ee0f2.jsonl")
	line := func(role, text string) string {
		return `{"type":"` + role + `","isSidechain":true,"agentId":"a768d0f31f74ee0f2","attributionAgent":"caveman:cavecrew-reviewer","sessionId":"` + parentID + `","timestamp":"2026-08-18T15:29:17.173Z","cwd":"/work/app","message":{"role":"` + role + `","content":"` + text + `"}}` + "\n"
	}
	if err := os.WriteFile(path, []byte(line("user", "review the diff")+line("assistant", "two findings")), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseClaudeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	s := ss[0]
	if s.ID != "a768d0f31f74ee0f2" {
		t.Errorf("id = %q — the child took the parent's id and the two collide", s.ID)
	}
	if s.Parent != parentID {
		t.Errorf("parent = %q, want the session that launched it", s.Parent)
	}
	if s.Kind != "sidechain" || s.Agent != "caveman:cavecrew-reviewer" {
		t.Errorf("kind = %q agent = %q", s.Kind, s.Agent)
	}
	if len(s.Messages) != 2 {
		t.Errorf("the child's turns did not survive: %#v", s.Messages)
	}

	// The generic parser is the reference the typed one is proved against, so
	// it has to read the same file the same way.
	ref, err := parseClaudeGenericFromOffset(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ref) != 1 || ref[0].ID != s.ID || ref[0].Parent != s.Parent || ref[0].Kind != s.Kind || ref[0].Agent != s.Agent {
		t.Errorf("reference parser disagrees: %#v", ref)
	}

	// A parent transcript is untouched: no kind, no parent, its own id.
	top := filepath.Join(root, "-work-app", parentID+".jsonl")
	plain := `{"type":"user","sessionId":"` + parentID + `","timestamp":"2026-08-18T15:00:00.000Z","cwd":"/work/app","message":{"role":"user","content":"launch a reviewer"}}` + "\n"
	if err := os.WriteFile(top, []byte(plain), 0o644); err != nil {
		t.Fatal(err)
	}
	ps, err := ParseClaudeFile(top)
	if err != nil || len(ps) != 1 {
		t.Fatalf("parent: %#v err=%v", ps, err)
	}
	if ps[0].ID != parentID || ps[0].Kind != "" || ps[0].Parent != "" {
		t.Errorf("an ordinary session was marked as spawned: %#v", ps[0])
	}
}
