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

// A promoted note is reached on the command path through "a session that ran
// this command said it", which is not "this is what happened last time you ran
// it". Read from a real store: `npm root -g` came back as "This machine has run
// that command in 2 sessions, last 2026-08-21 — last time: Описание репозитория
// переписано словами задачи…", the one accepted note on the machine and a rule
// about the repository description. Three runs, the same line (#3001).
//
// The file path learned this in #3451; the command path is the same rule.
func TestACommandDoesNotOwnANoteAboutSomethingElse(t *testing.T) {
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
		return `{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + when +
			`","cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"` + cmd + `"}}]}}`
	}
	// The session that gets promoted ran the command, and decided something
	// that has nothing to do with it.
	write("dec", []string{
		`{"type":"user","sessionId":"dec","timestamp":"` + at(401) + `","cwd":"/work/app","message":{"role":"user","content":"what should the public description of the repository say?"}}`,
		bash("dec", at(400), "npm root -g"),
		`{"type":"assistant","sessionId":"dec","timestamp":"` + at(399) + `","cwd":"/work/app","message":{"role":"assistant","content":"the description leads with the task, not the category"}}`,
	})
	for k := 0; k < 4; k++ {
		sid := fmt.Sprintf("r%d", k)
		write(sid, []string{
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + at(200-20*k) + `","cwd":"/work/app","message":{"role":"user","content":"where are the global packages"}}`,
			bash(sid, at(199-20*k), "npm root -g"),
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "promote", "dec", "--state", "accepted",
		"--note", "the repository description leads with the task rather than the category"); err != nil {
		t.Fatal(err)
	}

	line := commandHookLine(dir, "/work/app", "npm root -g")
	if line == "" {
		t.Fatal("the fixture produced no line at all")
	}
	if !strings.Contains(line, "repository description") {
		t.Fatalf("the promoted note is not carried at all, so this measures nothing:\n  %s", line)
	}
	if strings.Contains(line, "last time:") {
		t.Errorf("a note about something else is offered as what happened last time:\n  %s", line)
	}
	if !strings.Contains(line, "standing decision in this project") {
		t.Errorf("the note is not introduced for what it is:\n  %s", line)
	}
}

// And the other side: a note that does name the command keeps the stronger
// wording, because there it is true.
func TestACommandKeepsANoteThatNamesIt(t *testing.T) {
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
	bash := func(sid, when string) string {
		return `{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + when +
			`","cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"make migrate-orders"}}]}}`
	}
	write("dec", []string{
		`{"type":"user","sessionId":"dec","timestamp":"` + at(401) + `","cwd":"/work/app","message":{"role":"user","content":"should the migration run against production?"}}`,
		bash("dec", at(400)),
		`{"type":"assistant","sessionId":"dec","timestamp":"` + at(399) + `","cwd":"/work/app","message":{"role":"assistant","content":"against the replica first"}}`,
	})
	// The head of the line needs the command to be a habit: a single sighting
	// is not worth a line at all, so without these the test would pass on
	// silence.
	for k := 0; k < 5; k++ {
		sid := fmt.Sprintf("m%d", k)
		write(sid, []string{
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + at(300-20*k) + `","cwd":"/work/app","message":{"role":"user","content":"run the orders migration again"}}`,
			bash(sid, at(299-20*k)),
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "promote", "dec", "--state", "accepted",
		"--note", "make migrate-orders runs against the replica first; production needs the pool drained"); err != nil {
		t.Fatal(err)
	}

	line := commandHookLine(dir, "/work/app", "make migrate-orders")
	if !strings.Contains(line, "last time:") {
		t.Errorf("a note naming the command lost the wording it earned:\n  %s", line)
	}
}
