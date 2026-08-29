package main

import (
	"os"
	"strings"
	"testing"
)

// `deja help` lists hook-prompt, hook-plan, hook-tool and hook-tool-after, so
// people type them. Each reads a JSON payload from the harness; typed at a
// terminal they wait out the stdin bound and print nothing, which reads as
// "deja knows nothing" rather than "this one is not for you" (#2571).
func TestAHookTypedAtATerminalSaysWhatItIs(t *testing.T) {
	hermeticEnv(t)
	// A character device on stdin is what a terminal looks like; /dev/tty is
	// not available in CI, and os.Stdin under `go test` is not a terminal
	// either, so the check is exercised through its own helper.
	for _, name := range []string{"hook-prompt", "hook-tool", "hook-tool-after", "hook-plan"} {
		if note := hookTypedByHandNote(name, true); note == "" {
			t.Errorf("%s says nothing when typed at a terminal", name)
		} else if !strings.Contains(note, name) {
			t.Errorf("%s: note does not name the command: %q", name, note)
		}
	}
	// Piped, nothing changes: this is the path every harness uses.
	if note := hookTypedByHandNote("hook-prompt", false); note != "" {
		t.Errorf("a piped hook printed %q", note)
	}
	// And the real stdin under test is a pipe or a file, never a terminal, so
	// the helper agrees with the world it runs in.
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		t.Skip("stdin is a terminal here")
	}
}
