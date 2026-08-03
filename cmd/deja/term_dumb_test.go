package main

import (
	"os"
	"testing"
)

// TERM=dumb is a terminal that can do none of this — emacs shell-mode, a CI
// shell, an editor's built-in console. NO_COLOR was honoured and this was not,
// so those readers got escape sequences as literal text (#903).
func TestDumbTerminalGetsNoDrawing(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })

	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	if !defaultLogoWanted(f) {
		t.Fatal("a character device with a real TERM stopped being drawable")
	}
	t.Setenv("TERM", "dumb")
	if defaultLogoWanted(f) {
		t.Error("TERM=dumb still draws")
	}
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")
	if defaultLogoWanted(f) {
		t.Error("NO_COLOR stopped being honoured")
	}
}
