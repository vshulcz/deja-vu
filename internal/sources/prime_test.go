package sources

import (
	"fmt"
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

// rlm.spawn writes each child as its own transcript, but beside the session
// root under session-artifacts/<parent>/sub-<n>/, not in sessions/. Walking the
// root alone left every child unindexed (#4407).
func TestPrimeIndexesSpawnedChildrenAsSubagents(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "sessions")
	t.Setenv("DEJA_PRIME_ROOT", root)
	parent := "01a0f994-dbc6-75ac-9517-efe6eac2e13e"
	writePrimeSession(t, root, parent, "/tmp/proj-prime-g")
	sub := filepath.Join(base, "session-artifacts", parent, "sub-820d576b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(sub, "01a0f994-e172-71c9-b9fd-834f9bd5836e.jsonl")
	lines := `{"type":"session","version":3,"id":"01a0f994-e172-71c9-b9fd-834f9bd5836e","timestamp":"2026-10-01T22:27:58.000Z","cwd":"/private/tmp/proj-prime-g","parentSession":"~/.prime/agent/sessions/` + parent + `.jsonl","rlmDepth":1}
{"type":"message","id":"u1","timestamp":"2026-10-01T22:27:59.000Z","message":{"role":"user","content":[{"type":"text","text":"check the config"}]}}
{"type":"message","id":"t1","timestamp":"2026-10-01T22:28:01.000Z","message":{"role":"toolResult","content":[{"type":"text","text":"BashResult(exit_code=0, output='1:retries = 5\\n', duration=0.25296541582793)\n"}]}}
`
	if err := os.WriteFile(child, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	// prime's event log sits beside the child and is not a transcript.
	edges := `{"type":"session_registered","session_id":"01a0f994-e172-71c9-b9fd-834f9bd5836e","parent_session_id":"` + parent + `"}` + "\n"
	for _, dir := range []string{sub, filepath.Dir(sub)} {
		if err := os.WriteFile(filepath.Join(dir, "semantic-edges.jsonl"), []byte(edges), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	files := PrimeSessionFiles()
	if len(files) != 2 {
		t.Fatalf("files = %v, want the parent and the child", files)
	}
	if KindForPath(child) != "prime" {
		t.Errorf("KindForPath(child) = %q, want prime", KindForPath(child))
	}
	var got []string
	for _, s := range LoadPrime() {
		got = append(got, s.ID)
		if s.ID != "01a0f994-e172-71c9-b9fd-834f9bd5836e" {
			continue
		}
		if s.Kind != "subagent" || s.Parent != parent {
			t.Errorf("child kind/parent = %q/%q, want subagent/%s", s.Kind, s.Parent, parent)
		}
	}
	if len(got) != 2 {
		t.Fatalf("sessions = %v, want the parent and the child", got)
	}
}

// A spawned child is a subagent like Claude Code's, and comes in the same way:
// the task, what it changed and how it ended, the reading in between left out,
// unless DEJA_INCLUDE_SUBAGENTS=1 asks for the whole run; =0 skips children.
// Indexed whole, a child's exploring competed with its parent for recall
// (#3009) and DEJA_INCLUDE_SUBAGENTS did nothing for prime (#4407).
func TestPrimeChildrenFollowTheSubagentSwitch(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "sessions")
	t.Setenv("DEJA_PRIME_ROOT", root)
	parent := "01a0f994-dbc6-75ac-9517-efe6eac2e13e"
	writePrimeSession(t, root, parent, "/tmp/proj-prime-g")
	sub := filepath.Join(base, "session-artifacts", parent, "sub-820d576b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(sub, "01a0f994-e172-71c9-b9fd-834f9bd5836e.jsonl")
	lines := `{"type":"session","version":3,"id":"01a0f994-e172-71c9-b9fd-834f9bd5836e","cwd":"/tmp/proj-prime-g","parentSession":"~/.prime/agent/sessions/` + parent + `.jsonl","rlmDepth":1}` + "\n" +
		`{"type":"message","id":"u1","timestamp":"2026-10-01T22:27:59.000Z","message":{"role":"user","content":[{"type":"text","text":"check the config"}]}}` + "\n"
	for i := range 8 {
		lines += fmt.Sprintf(`{"type":"message","id":"a%d","timestamp":"2026-10-01T22:28:%02d.000Z","message":{"role":"assistant","content":[{"type":"text","text":"looking at part %d"}]}}`, i, i, i) + "\n"
	}
	lines += `{"type":"message","id":"z","timestamp":"2026-10-01T22:29:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"retries is 5"}]}}` + "\n"
	if err := os.WriteFile(child, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	texts := func() []string {
		ss, err := ParsePrimeFile(child)
		if err != nil || len(ss) != 1 {
			t.Fatalf("parse child: %v, %d sessions", err, len(ss))
		}
		var out []string
		for _, m := range ss[0].Messages {
			out = append(out, m.Text)
		}
		return out
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	got := texts()
	if len(got) != SubagentTailKept+1 || got[0] != "check the config" || got[len(got)-1] != "retries is 5" {
		t.Errorf("default: %q, want the task and the last %d turns", got, SubagentTailKept)
	}
	if !IsPrimeChildPath(child) || IsPrimeChildPath(filepath.Join(root, parent+".jsonl")) {
		t.Error("IsPrimeChildPath does not tell the child from the parent")
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	if got := texts(); len(got) != 10 {
		t.Errorf("DEJA_INCLUDE_SUBAGENTS=1: %d messages, want all 10", len(got))
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	if files := PrimeSessionFiles(); len(files) != 1 || IsPrimeChildPath(files[0]) {
		t.Errorf("DEJA_INCLUDE_SUBAGENTS=0: files = %v, want the parent alone", files)
	}
	if KindForPath(child) == "prime" {
		t.Error("DEJA_INCLUDE_SUBAGENTS=0: the child still matches for incremental ingest")
	}
}
