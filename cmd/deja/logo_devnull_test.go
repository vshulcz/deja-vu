package main

import (
	"os"
	"runtime"
	"testing"
)

// The null device is a character device, so the mode test alone took it for a
// terminal. `deja index >/dev/null` then chose the live display, painted it
// into the sink that discards it, and printed none of the plain lines on
// stderr — which is exactly the output someone redirecting stdout away is
// keeping. Measured before the fix: stderr 0 bytes with stdout on /dev/null,
// 92 bytes with stdout on a file, same binary and same store.
func TestNullDeviceIsNotATerminal(t *testing.T) {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	// Guards the premise: if the null device ever stops being a character
	// device, this test is proving nothing and should say so rather than pass.
	if fi.Mode()&os.ModeCharDevice == 0 && runtime.GOOS != "windows" {
		t.Fatalf("%s is not a character device here, so it never reached the branch under test", os.DevNull)
	}
	if logoWanted(f) {
		t.Fatalf("%s must not be taken for a terminal: the live display goes to the sink and the plain lines are suppressed", os.DevNull)
	}
}

// And the exclusion must be the null device only, not every character device:
// a real terminal still gets the mark and the progress bar.
func TestARealTerminalStillWantsTheLogo(t *testing.T) {
	f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		t.Skip("no controlling terminal here")
	}
	defer func() { _ = f.Close() }()
	if !logoWanted(f) {
		t.Fatal("a real terminal must still want the logo")
	}
}
