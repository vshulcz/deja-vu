package main

import (
	"bytes"
	"strings"
	"testing"
)

// The table is headed "Hooks" and every row read "wired", including two that
// are not hooks. aider's file is a context file that only `deja aider`
// refreshes — bare aider reads whatever was written last. roo's is guidance:
// the agent is asked to call recall, and hands itself nothing if it does not.
//
// Someone reading a row that says wired concludes memory arrives on its own.
// For those two it does not, and the whole point of this command is to be the
// thing a person trusts when memory is not working.
func TestTheHooksTableSaysWhichRowsAreNotHooks(t *testing.T) {
	var out bytes.Buffer
	doctorAutoRecall(&out)
	got := out.String()

	for _, want := range []struct{ name, says string }{
		{"aider", "context file"},
		{"roo", "guidance"},
	} {
		line := ""
		for _, l := range strings.Split(got, "\n") {
			if strings.Contains(l, want.name+" ") {
				line = l
			}
		}
		if line == "" {
			t.Errorf("%s has no row at all", want.name)
			continue
		}
		if !strings.Contains(line, want.says) {
			t.Errorf("%s reads as a hook like any other:\n  %s", want.name, line)
		}
	}

	// And a real hook carries no such note, or the distinction says nothing.
	for _, l := range strings.Split(got, "\n") {
		if strings.Contains(l, "opencode ") &&
			(strings.Contains(l, "context file") || strings.Contains(l, "guidance")) {
			t.Errorf("a real hook was annotated as something else:\n  %s", l)
		}
	}
}
