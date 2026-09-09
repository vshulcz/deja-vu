package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The hook gets one line per action, and it was spending some of them on a
// count and a date: "This machine has run that command in 5 sessions, last
// 2026-05-21." The comment beside the code that builds it says why that is not
// worth saying — a line that only says history exists changes nothing — and on
// a real store two of the four lines it produced were exactly that (#3415).
func TestTheCommandHookSaysNothingWithoutAnOutcome(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// Sessions that ran the command and concluded nothing about it.
	for _, id := range []string{"a", "b", "c"} {
		writeClaudeFixture(t, filepath.Join(root, "-work-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"push it"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"git push origin main"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	if got := commandHookLine(os.Getenv("DEJA_INDEX_DIR"), "/work/app", "git push origin main"); got != "" {
		t.Errorf("the hook spent its line on a bare count: %q", got)
	}

	// The same store, one session that says how it went: that line is worth
	// the action it interrupts.
	writeClaudeFixture(t, filepath.Join(root, "-work-app", "d.jsonl"), "d", []string{
		`{"type":"user","sessionId":"d","cwd":"/work/app","timestamp":"2026-01-03T03:04:05Z","message":{"role":"user","content":"the push to origin main keeps being rejected"}}`,
		`{"type":"assistant","sessionId":"d","cwd":"/work/app","timestamp":"2026-01-03T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"git push origin main"}}]}}`,
		`{"type":"assistant","sessionId":"d","cwd":"/work/app","timestamp":"2026-01-03T03:04:07Z","message":{"role":"assistant","content":"the push has to go to a branch: origin main is protected and rejects a direct push."}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	got := commandHookLine(os.Getenv("DEJA_INDEX_DIR"), "/work/app", "git push origin main")
	if !strings.Contains(got, "last time:") {
		t.Errorf("the outcome did not reach the line: %q", got)
	}
}
