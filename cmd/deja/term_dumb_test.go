package main

import (
	"os"
	"runtime"
	"testing"
)

// drawableCharDevice returns a character device standing in for a terminal.
//
// This used to be os.DevNull, which is a character device and was the handiest
// one. It cannot be any more: the null device is excluded on purpose now,
// because taking it for a terminal made `deja index >/dev/null` paint the live
// display into the sink and print none of the plain lines on stderr. The
// subject of this test is TERM and NO_COLOR, not which device stands in, so
// the stand-in moves and the assertions stay as they were.
func drawableCharDevice(t *testing.T) *os.File {
	t.Helper()
	name := "/dev/zero"
	if runtime.GOOS == "windows" {
		// NUL is excluded there too since #1097's windows half, so the console
		// input device is the only stand-in left; a runner without a console
		// skips.
		name = "CONIN$"
	}
	f, err := os.Open(name)
	if err != nil {
		t.Skipf("no character device to stand in for a terminal: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeCharDevice == 0 {
		t.Skipf("%s is not a character device here", name)
	}
	return f
}

// TERM=dumb is a terminal that can do none of this — emacs shell-mode, a CI
// shell, an editor's built-in console. NO_COLOR was honoured and this was not,
// so those readers got escape sequences as literal text (#903).
func TestDumbTerminalGetsNoDrawing(t *testing.T) {
	f := drawableCharDevice(t)

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
