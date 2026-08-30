package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/policy"
)

// ignoredCommandStore holds a command run only in a tree the ignore rule keeps
// out of recall, and one kept session that never ran it.
func ignoredCommandStore(t *testing.T, command string) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	if err := os.MkdirAll(filepath.Dir(policy.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policy.Path(), []byte(`{"ignore":["*scratch*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"s1", "s2", "s3"} {
		writeClaudeFixture(t, filepath.Join(root, "-w-scratch", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"deploy the thing"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"` + command + `"}}]}}`,
		})
	}
	writeClaudeFixture(t, filepath.Join(root, "-w-keep", "k1.jsonl"), "k1", []string{
		`{"type":"user","sessionId":"k1","cwd":"/w/keep","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"morning notes"}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
}

// #2630 taught how, restore and friction the ignore rule. The hook that speaks
// unasked was left: it read the commands table, which keys by project, and
// stated a count out of the tree the reader excluded (#2652).
func TestTheCommandHookObeysTheIgnoreRule(t *testing.T) {
	ignoredCommandStore(t, "terraform apply -auto-approve")
	out := commandHookLine(os.Getenv("DEJA_INDEX_DIR"), "/w/keep", "terraform apply -auto-approve")
	if strings.Contains(out, "has run that command") {
		t.Fatalf("the hook spoke from the tree the ignore rule keeps out:\n%s", out)
	}
	if out != "" {
		t.Fatalf("nothing else is left to say about it:\n%s", out)
	}
}

// A command run where recall may go keeps its line.
func TestTheCommandHookStillSpeaksForAKeptProject(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"k1", "k2"} {
		writeClaudeFixture(t, filepath.Join(root, "-w-keep", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/keep","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"deploy the thing"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/keep","timestamp":"2026-07-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"terraform apply -auto-approve"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	out := commandHookLine(os.Getenv("DEJA_INDEX_DIR"), "/w/keep", "terraform apply -auto-approve")
	if !strings.Contains(out, "has run that command") {
		t.Fatalf("the hook went quiet about a command it may speak of:\n%s", out)
	}
}
