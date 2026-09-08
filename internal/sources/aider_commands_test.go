package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// aider writes every input as a `#### ` line, its own commands included, so
// `/undo` became a user message of its own and `/clear` was glued onto the
// question typed after it (#3248).
func TestAiderDropsItsOwnCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.md")
	const doc = `# aider chat started at 2026-08-01 10:00:00

#### /clear
#### why does the vantrell fetcher time out

the pool is exhausted before the second attempt

#### /undo
#### /add internal/retry/retry.go
#### and what did we settle about the retry cap

four, and the backoff is on the deadline

#### /etc/hosts is wrong on the build box

that is a different machine
`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAiderFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	var users []string
	for _, m := range ss[0].Messages {
		if m.Role == "user" {
			users = append(users, m.Text)
		}
	}
	want := []string{
		"why does the vantrell fetcher time out",
		"and what did we settle about the retry cap",
		// A message that merely opens with a path is the person's.
		"/etc/hosts is wrong on the build box",
	}
	if strings.Join(users, "|") != strings.Join(want, "|") {
		t.Fatalf("user turns =\n  %s\nwant\n  %s",
			strings.Join(users, "\n  "), strings.Join(want, "\n  "))
	}
	// And the answers are untouched.
	found := false
	for _, m := range ss[0].Messages {
		if m.Role == "assistant" && strings.Contains(m.Text, "pool is exhausted") {
			found = true
		}
	}
	if !found {
		t.Error("an answer went with the commands")
	}
}

// The classifier itself, since the list is what decides.
func TestAiderSlashCommandKnowsWhatIsACommand(t *testing.T) {
	for _, cmd := range []string{
		"/undo", "/clear", "/add internal/retry/retry.go", "/run go test ./...",
		"/drop", "/model gpt-5", "/tokens", "  /quit  ", "/GIT status",
	} {
		if !aiderSlashCommand(cmd) {
			t.Errorf("%q is one of aider's commands", cmd)
		}
	}
	for _, mine := range []string{
		"/etc/hosts is wrong on the build box",
		"/usr/local/bin/deja is on PATH now",
		"/ask why the retry cap is four",
		"/code add a backoff",
		"/",
		"why does /undo not work here",
		"",
	} {
		if aiderSlashCommand(mine) {
			t.Errorf("%q is the reader's, not a command", mine)
		}
	}
}
