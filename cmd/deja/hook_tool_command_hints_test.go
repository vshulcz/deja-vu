package main

import "testing"

// The line before a command is opt-in. The file line before an edit is not
// touched by the switch.
func TestCommandHintsAreOffUnlessAskedFor(t *testing.T) {
	for _, v := range []string{"", "off", "1", "yes"} {
		t.Setenv("DEJA_COMMAND_HINTS", v)
		if commandHintsOn() {
			t.Errorf("DEJA_COMMAND_HINTS=%q turned the command line on", v)
		}
		in := toolHookInput{ToolName: "Bash"}
		in.ToolInput.Command = "go test ./..."
		if got := toolHookLine(t.TempDir(), t.TempDir(), in); got != "" {
			t.Errorf("DEJA_COMMAND_HINTS=%q: a command drew a line: %q", v, got)
		}
	}
	for _, v := range []string{"on", "ON", " on "} {
		t.Setenv("DEJA_COMMAND_HINTS", v)
		if !commandHintsOn() {
			t.Errorf("DEJA_COMMAND_HINTS=%q left the command line off", v)
		}
	}
}
