package index

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// codex and opencode record what a command exited with, appended to the command
// line itself: "$ make test  → exit 2". That is the outcome, not the invocation,
// and the commands table keyed on the whole line — so one command became two
// rows, the runs that worked and the runs that did not, counted apart. On this
// machine's index four commands are split that way, `git status --short` into
// 445 runs and 7 (#2590).
func TestTheCommandsTableIgnoresTheExitStatus(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	msg := func(text string, min int) model.Message {
		return model.Message{Role: roleCommand, Text: text, Time: at.Add(time.Duration(min) * time.Minute)}
	}
	sessions := []model.Session{
		{ID: "s1", Harness: "codex", Project: "app", Messages: []model.Message{
			msg("$ make test", 0),
			msg("$ make test  → exit 2", 1),
		}},
		{ID: "s2", Harness: "codex", Project: "app", Messages: []model.Message{
			msg("$ make test  → exit 2", 2),
		}},
	}
	buildCommands(dir, sessions)
	uses := ReadCommands(dir)
	var found []CommandUse
	for _, u := range uses {
		if strings.Contains(strings.ToLower(u.Command), "make test") {
			found = append(found, u)
		}
	}
	if len(found) != 1 {
		var got []string
		for _, u := range found {
			got = append(got, u.Command)
		}
		t.Fatalf("make test is %d rows, want one: %v", len(found), got)
	}
	if found[0].Runs != 3 || found[0].Sessions != 2 {
		t.Errorf("runs=%d sessions=%d, want 3 runs in 2 sessions", found[0].Runs, found[0].Sessions)
	}
	if strings.Contains(found[0].Command, "exit") {
		t.Errorf("the row deja shows carries the outcome: %q", found[0].Command)
	}
	// And the lookup the tool hook makes, with the command as a harness hands
	// it over, finds all of it.
	use, ok := CommandHistory(dir, "make test")
	if !ok || use.Runs != 3 {
		t.Errorf("CommandHistory(make test) = %+v ok=%v", use, ok)
	}
}
