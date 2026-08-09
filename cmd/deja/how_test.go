package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Commands are kept out of ranking on purpose, which left the most reusable
// thing in a store — the invocation that actually works on this machine —
// unreachable. `how` is that channel, and it weighs a command by how many
// separate sessions ran it rather than by how often one session repeated it.
func TestHowRanksByHowManySessionsRanIt(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// One session hammering the same command.
	writeClaudeFixture(t, filepath.Join(root, "p", "loop.jsonl"), "loop", []string{
		`{"type":"user","sessionId":"loop","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"run the suite"}}`,
		`{"type":"assistant","sessionId":"loop","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./internal/loop -run TestOne"}}]}}`,
		`{"type":"assistant","sessionId":"loop","timestamp":"2026-01-02T03:04:07Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go test ./internal/loop -run TestOne"}}]}}`,
		`{"type":"assistant","sessionId":"loop","timestamp":"2026-01-02T03:04:08Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t3","name":"Bash","input":{"command":"go test ./internal/loop -run TestOne"}}]}}`,
	})
	// Two separate sessions running the project's real invocation once each.
	for i, id := range []string{"a", "b"} {
		writeClaudeFixture(t, filepath.Join(root, "p", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-0` + string(rune('3'+i)) + `T03:04:05Z","message":{"role":"user","content":"run the suite"}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"2026-01-0` + string(rune('3'+i)) + `T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./... -count=1"}}]}}`,
		})
	}
	out, err := captureRun(t, "how", "go test")
	if err != nil {
		t.Fatal(err)
	}
	first := strings.SplitN(strings.TrimSpace(out), "\n", 2)[0]
	if !strings.Contains(first, "-count=1") {
		t.Errorf("the command two sessions ran is not first:\n%s", out)
	}
	if !strings.Contains(out, "2 sessions") {
		t.Errorf("the session count is not reported:\n%s", out)
	}
}

func TestHowSaysNothingRatherThanGuessing(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "s.jsonl"), "s", []string{
		`{"type":"user","sessionId":"s","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"hello"}}`,
	})
	out, err := captureRun(t, "how", "terraform apply")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no command on this machine mentions") {
		t.Errorf("a store with no such command must say so, got:\n%s", out)
	}
}
