package sources

import (
	"os"
	"path/filepath"
	"testing"
)

func writePrimeSession(t *testing.T, root, id, cwd string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-08-30T09:00:00.000Z","cwd":"` + cwd + `","parentSession":null,"rlmDepth":0}
{"type":"model_change","id":"m1","parentId":null,"timestamp":"2026-08-30T09:00:01.000Z","model":"prime/rlm-1"}
{"type":"message","id":"u1","parentId":"m1","timestamp":"2026-08-30T09:01:00.000Z","message":{"role":"user","content":[{"type":"text","text":"why does the telemetry parser drop frames"}]}}
{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-08-30T09:02:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"the ring buffer is capped at 4096 frames"}]}}
`
	path := filepath.Join(root, id+".jsonl")
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The root is flat — one file per session, named by uuid7 — so there is no
// encoded project directory to read a name out of. The header's cwd is what
// names the project, which is the whole reason this parser passes
// useHeaderCwd.
func TestPrimeTakesItsProjectFromTheHeader(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv("DEJA_PRIME_ROOT", root)
	writePrimeSession(t, root, "0192f3a1-7c31-7000-8000-abcdef012345", "/Users/x/code/rocketapp")

	got := LoadPrime()
	if len(got) != 1 {
		t.Fatalf("sessions = %d, want 1", len(got))
	}
	s := got[0]
	if s.Harness != "prime" {
		t.Errorf("harness = %q", s.Harness)
	}
	if s.Project != "code/rocketapp" {
		t.Errorf("project = %q, want it read from the header cwd", s.Project)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %d, want the user turn and the answer", len(s.Messages))
	}
	if s.Messages[1].Text != "the ring buffer is capped at 4096 frames" {
		t.Errorf("answer text = %q", s.Messages[1].Text)
	}
}

// prime-agent relocates its own store with two variables, and a machine that
// has moved its sessions has moved them for deja too. DEJA_PRIME_ROOT still
// wins, for tests and for a store that is neither.
func TestPrimeReadsTheAgentsOwnRootVariables(t *testing.T) {
	moved := filepath.Join(t.TempDir(), "moved")
	t.Setenv("DEJA_PRIME_ROOT", "")
	t.Setenv("PRIME_AGENT_SESSION_DIR", moved)
	writePrimeSession(t, moved, "0192f3a1-7c31-7000-8000-000000000001", "/Users/x/code/moved")
	if got := PrimeRoot(); got != moved {
		t.Fatalf("root = %q, want the agent's own variable honoured", got)
	}
	if len(LoadPrime()) != 1 {
		t.Error("a relocated store came back empty")
	}

	own := filepath.Join(t.TempDir(), "own")
	t.Setenv("DEJA_PRIME_ROOT", own)
	if got := PrimeRoot(); got != own {
		t.Errorf("root = %q, want DEJA_PRIME_ROOT to win", got)
	}
}
