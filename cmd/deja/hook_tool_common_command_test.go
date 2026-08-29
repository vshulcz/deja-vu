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

// Measured on a real index: of the fifteen commands this machine runs most, not
// one has every word clearing the idf floor — a command's words are common by
// construction, which is what makes it a command that gets run. The line
// therefore never reached a session at all (#2520).
func TestTheCommandLineAnswersAboutAnOrdinaryCommand(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	at := func(m int) string { return now.Add(-time.Duration(m) * time.Minute).Format(time.RFC3339) }
	write := func(sid string, lines []string) {
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bash := func(sid, when, cmd string) string {
		return `{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + when + `","cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"` + cmd + `"}}]}}`
	}
	// A session that concluded something about the command, older than the runs
	// below — the promoted-decision path (#2516) is what lifts this one; here
	// it is only part of the corpus.
	write("dec", []string{
		`{"type":"user","sessionId":"dec","timestamp":"` + at(401) + `","cwd":"/work/app","message":{"role":"user","content":"the integration suite keeps failing on the shared fixture"}}`,
		bash("dec", at(400), "go test ./..."),
		`{"type":"assistant","sessionId":"dec","timestamp":"` + at(399) + `","cwd":"/work/app","message":{"role":"assistant","content":"the suite has to run with -p 1: the shared fixture cannot take parallel packages."}}`,
	})
	// Sessions that ran the same command, making its words ordinary — which is
	// exactly what happens on a real machine.
	for k := 0; k < 8; k++ {
		sid := fmt.Sprintf("r%d", k)
		write(sid, []string{
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + at(300-10*k) + `","cwd":"/work/app","message":{"role":"user","content":"run the tests"}}`,
			bash(sid, at(299-10*k), "go test ./..."),
			`{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + at(298-10*k) + `","cwd":"/work/app","message":{"role":"assistant","content":"tests pass"}}`,
		})
	}
	// And unrelated work, so the store is not one topic.
	for i, top := range []string{"the sidebar layout", "the invoice renderer", "the webpack config",
		"the login throttle", "the avatar uploader", "the cron scheduler", "the email templates"} {
		sid := fmt.Sprintf("t%d", i)
		write(sid, []string{
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + at(900-10*i) + `","cwd":"/work/app","message":{"role":"user","content":"work on ` + top + `"}}`,
			`{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + at(899-10*i) + `","cwd":"/work/app","message":{"role":"assistant","content":"changed ` + top + `"}}`,
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	line := commandHookLine(dir, "/work/app", "go test ./...")
	if line == "" {
		t.Fatal("the fixture produced no line at all")
	}
	// What the surface promises: what happened the last time this command ran.
	if !strings.Contains(line, "last time:") {
		t.Errorf("the line reached no session at all:\n  %s", line)
	}
	if !strings.Contains(line, "tests pass") {
		t.Errorf("the line does not carry what the last run of this command concluded:\n  %s", line)
	}
}

// The precision the old rule was after: a session that shares one ordinary word
// with the command is not about it.
func TestTheCommandLineIgnoresASessionSharingOneOrdinaryWord(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	at := func(m int) string { return now.Add(-time.Duration(m) * time.Minute).Format(time.RFC3339) }
	write := func(sid string, lines []string) {
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Nobody ran `terraform apply` here; one session merely says "apply".
	write("other", []string{
		`{"type":"user","sessionId":"other","timestamp":"` + at(200) + `","cwd":"/work/app","message":{"role":"user","content":"apply the migration to the staging database"}}`,
		`{"type":"assistant","sessionId":"other","timestamp":"` + at(199) + `","cwd":"/work/app","message":{"role":"assistant","content":"the staging database now matches production."}}`,
	})
	for i, top := range []string{"the sidebar layout", "the invoice renderer", "the webpack config"} {
		sid := fmt.Sprintf("t%d", i)
		write(sid, []string{
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + at(900-10*i) + `","cwd":"/work/app","message":{"role":"user","content":"work on ` + top + `"}}`,
			`{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + at(899-10*i) + `","cwd":"/work/app","message":{"role":"assistant","content":"changed ` + top + `"}}`,
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	if line := commandDecisionLine(dir, "/work/app", "terraform apply"); line != "" {
		t.Errorf("a session sharing one ordinary word became this command's history: %q", line)
	}
}
