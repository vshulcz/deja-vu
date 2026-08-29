package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

func editStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	at := func(m int) string { return now.Add(-time.Duration(m) * time.Minute).Format(time.RFC3339) }
	for k := 0; k < 6; k++ {
		sid := fmt.Sprintf("f%d", k)
		lines := []string{
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + at(200-10*k) + `","cwd":"/work/app","message":{"role":"user","content":"work on the invoice renderer"}}`,
			`{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + at(199-10*k) + `","cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/app/render.go","old_string":"x","new_string":"y"}}]}}`,
			`{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + at(198-10*k) + `","cwd":"/work/app","message":{"role":"assistant","content":"changed the renderer (` + fmt.Sprint(k) + `)"}}`,
		}
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The line was built on a measurement: an agent follows a decision where it
// ignores a pointer. Calling the last sentence of the last session a "prior
// decision" spends that credibility on filler — and the same surface already
// says the honest weaker thing for commands (#2526).
func TestAScannedConclusionIsNotCalledADecision(t *testing.T) {
	dir := editStore(t)

	line := fileHookLine(dir, "/work/app", "/work/app/render.go")
	if line == "" {
		t.Fatal("the fixture produced no line at all")
	}
	if strings.Contains(line, "prior decision") {
		t.Errorf("a session's closing sentence is offered as a decision:\n  %s", line)
	}
	if !strings.Contains(line, "changed the renderer") {
		t.Errorf("the line stopped carrying what the last session ended on:\n  %s", line)
	}
}

// A promoted decision keeps the word, since that is what it is.
func TestAPromotedDecisionKeepsTheWord(t *testing.T) {
	dir := editStore(t)
	if _, err := captureRunStderr(t, "promote", "f5", "--state", "accepted",
		"--note", "the renderer never re-wraps a paragraph that already fits"); err != nil {
		t.Fatal(err)
	}

	line := fileHookLine(dir, "/work/app", "/work/app/render.go")
	if !strings.Contains(line, "prior decision: the renderer never re-wraps") {
		t.Errorf("a promoted decision is no longer named as one:\n  %s", line)
	}
}
