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
// ignores a pointer. The last sentence of the last session is not a decision,
// and offered under a weaker label it was never acted on either: a month of real
// sessions, 37 of those lines and no recall, no blame, no edit changed. So the
// line says nothing rather than spend the agent's context on it (#2526).
func TestAScannedConclusionIsNotCalledADecision(t *testing.T) {
	dir := editStore(t)

	if line := fileHookLine(dir, "/work/app", "/work/app/render.go"); line != "" {
		t.Errorf("a session's closing sentence reached the edit:\n  %s", line)
	}
}

// A note the user promoted reaches the file through any session that touched
// it, and one long session touches everything. Read from a real month: the one
// accepted note on the machine, a rule about the repository description, went
// out in front of 56 edits to Go files it says nothing about, and the
// session-start block already carries it. A note that does not name the file
// stays off this line.
func TestAProjectNoteThatIsNotAboutTheFileStaysOff(t *testing.T) {
	dir := editStore(t)
	if _, err := captureRunStderr(t, "promote", "f5", "--state", "accepted",
		"--note", "the repository description leads with the task, not the category"); err != nil {
		t.Fatal(err)
	}

	if line := fileHookLine(dir, "/work/app", "/work/app/render.go"); line != "" {
		t.Errorf("a note about something else went out in front of the edit:\n  %s", line)
	}
}

// A promoted decision keeps the word, since that is what it is — and says
// where it holds. A note is promoted in a session, not against a file, so it
// reaches this line through "a session that worked on this file said it", which
// is not "this was decided about this file". On a real store the one accepted
// note arrived in front of `main.go` reading as that file's decision, where it
// is a rule about the repository description.
func TestAPromotedDecisionKeepsTheWord(t *testing.T) {
	dir := editStore(t)
	if _, err := captureRunStderr(t, "promote", "f5", "--state", "accepted",
		"--note", "the renderer never re-wraps a paragraph that already fits"); err != nil {
		t.Fatal(err)
	}

	line := fileHookLine(dir, "/work/app", "/work/app/render.go")
	if !strings.Contains(line, "decision in this project: the renderer never re-wraps") {
		t.Errorf("a promoted decision is no longer named as one, or claims the file:\n  %s", line)
	}
}
