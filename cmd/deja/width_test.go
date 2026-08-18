package main

import (
	"os"
	"testing"
)

// Reading the terminal is best-effort: under `go test` stdout is a pipe, so the
// answer must be "no" rather than a made-up number. Every caller falls back when
// it hears no.
func TestTerminalWidthSaysNoWhenThereIsNoTerminal(t *testing.T) {
	if n, ok := terminalWidth(); ok {
		t.Errorf("a pipe reported a width of %d columns", n)
	}
}

// COLUMNS is an override, not a hint: a reader who exports it, or a script that
// pins the layout, decides.
func TestBriefWidthPrefersColumns(t *testing.T) {
	t.Setenv("COLUMNS", "60")
	if got := briefWidth(); got != 60 {
		t.Errorf("briefWidth = %d with COLUMNS=60", got)
	}
}

// Nonsense in COLUMNS is ignored rather than obeyed, and with no terminal to ask
// the answer is the old default.
func TestBriefWidthFallsBackTo80(t *testing.T) {
	for _, v := range []string{"", "wide", "0", "5", "100000"} {
		if v == "" {
			if err := os.Unsetenv("COLUMNS"); err != nil {
				t.Fatal(err)
			}
		} else {
			t.Setenv("COLUMNS", v)
		}
		if got := briefWidth(); got != 80 {
			t.Errorf("COLUMNS=%q: briefWidth = %d, want the 80-column default", v, got)
		}
	}
}
