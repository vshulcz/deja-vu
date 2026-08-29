package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// roleServedStore holds a topic that exists only in records served by role: a
// command that was run and a file that was edited, never a word of prose.
func roleServedStore(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"s1", "s2", "s3"} {
		writeClaudeFixture(t, filepath.Join(root, "-w-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/app","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"carry on"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/app","timestamp":"2026-07-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"make widgetpipeline"}}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/app","timestamp":"2026-07-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/app/widgetpipeline.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
}

// Commands, file lists and edits never reach ranking, on purpose. So a search
// for a word that lives only there misses — while `how` and `blame` answer it —
// and "try fewer words" is counsel no rewording can follow (#2634).
func TestAMissPointsAtTheRecordsServedByRole(t *testing.T) {
	roleServedStore(t)
	out, err := captureRunStderr(t, "widgetpipeline")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no matches") {
		t.Fatalf("the fixture no longer misses, so this pins nothing:\n%s", out)
	}
	if !strings.Contains(out, "deja how") {
		t.Fatalf("nothing says a command on this machine matches it:\n%s", out)
	}
	if !strings.Contains(out, "deja blame") {
		t.Fatalf("nothing says a file on this machine matches it:\n%s", out)
	}
}

// A miss that really is a miss keeps its short answer: a line on every failed
// search is noise that teaches people to skip the last line.
func TestAnOrdinaryMissSaysNothingExtra(t *testing.T) {
	roleServedStore(t)
	out, err := captureRunStderr(t, "zonkomatic")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "deja how") || strings.Contains(out, "deja blame") {
		t.Fatalf("a word this machine never saw was pointed somewhere:\n%s", out)
	}
}
