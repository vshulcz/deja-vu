package index

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// frictionIgnoreStore gives the same wall to three servable sessions and two the
// ignore rule keeps out.
func frictionIgnoreStore(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	policyFile := filepath.Join(home, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*scratch*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	write := func(project, id string) {
		p := filepath.Join(root, "-w-"+project, id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","sessionId":"` + id + `","cwd":"/w/` + project + `","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the widget pipeline keeps stalling"}}` + "\n" +
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/` + project + `","timestamp":"2026-07-01T10:02:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"make: *** [widget] Error 2"}]}}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		write("keep", fmt.Sprintf("k%d", i))
	}
	for i := 0; i < 2; i++ {
		write("scratch", fmt.Sprintf("s%d", i))
	}
	dir := filepath.Join(home, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TopFriction gates on the caller's allow(project), which is the trust policy.
// The ignore rule was not applied and cannot be applied by the callers — allow
// takes a project and the rule matches on the path — so the brief said five
// sessions where `deja friction` said three, and the session-start block
// counted the same two (#2658).
func TestTopFrictionLeavesOutTheIgnoredTree(t *testing.T) {
	dir := frictionIgnoreStore(t)
	walls := TopFriction(dir, 5, nil)
	if len(walls) != 1 {
		t.Fatalf("one wall is shared by every session here, got %d", len(walls))
	}
	if n := len(walls[0].Sessions); n != 3 {
		t.Fatalf("three sessions recall may serve hit that wall, the wall claims %d", n)
	}
	for _, s := range walls[0].Sessions {
		if s.Project == "w/scratch" {
			t.Fatalf("a session the ignore rule keeps out is evidence for the wall: %+v", s)
		}
	}
}

// A wall that only the ignored tree hits is not this machine's friction at all.
func TestTopFrictionDropsAWallOnlyTheIgnoredTreeHits(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	policyFile := filepath.Join(home, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*scratch*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("s%d", i)
		p := filepath.Join(root, "-w-scratch", id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"deploying"}}` + "\n" +
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:02:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"make: *** [widget] Error 2"}]}}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(home, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if walls := TopFriction(dir, 5, nil); len(walls) != 0 {
		t.Fatalf("a wall only the ignored tree hits is not this machine's friction: %+v", walls)
	}
}
