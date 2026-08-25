package main

import (
	"os"
	"strings"
	"testing"
)

// The documented install is `curl -fsSL … | sh`, which makes stdin the script
// itself: [ -t 0 ] is false on a real terminal, so a guard written that way puts
// the PATH offer behind a condition no reader of the README can satisfy. Every
// one of them gets ACTION REQUIRED and edits a dotfile by hand instead — on
// macOS, where ~/.local/bin is not on PATH by default, that is the step between
// installing deja and being able to run it.
//
// The offer reads from the controlling terminal instead. No terminal at all —
// CI, a Dockerfile — still falls through to the message and never blocks.
func TestInstallerAsksThroughTheControllingTerminal(t *testing.T) {
	b, err := os.ReadFile("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	// Comments are allowed to name the old condition; code is not.
	var code []string
	for _, line := range strings.Split(string(b), "\n") {
		if trimmed := strings.TrimSpace(line); !strings.HasPrefix(trimmed, "#") {
			code = append(code, line)
		}
	}
	src := strings.Join(code, "\n")

	if strings.Contains(src, "[ -t 0 ]") {
		t.Error("install.sh gates something on stdin being a terminal; under `curl | sh` it never is")
	}
	if !strings.Contains(src, "read -r answer < /dev/tty") {
		t.Error("the PATH offer does not read from /dev/tty, so a piped install cannot answer it")
	}
	// Prompting with no terminal to prompt on would hang a CI install.
	if !strings.Contains(src, `[ -t 1 ] && [ -r /dev/tty ]`) {
		t.Error("the offer is not guarded by a writable terminal and a readable /dev/tty")
	}
}
