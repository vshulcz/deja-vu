package main

import (
	"os/exec"
	"strings"
	"testing"
)

// Claude Code runs hook commands through bash, Git Bash on Windows, and bash
// eats an unquoted backslash: `H:\pycode\Self\deja-vu\deja.exe` reached it as
// `H:pycodeSelfdeja-vudeja.exe`, every hook exited 127 and nothing said so
// (#4116). What deja writes on Windows has to reach bash as the same file.
func TestAWindowsHookPathSurvivesBash(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	for _, path := range []string{
		`H:\pycode\Self\deja-vu\deja.exe`,
		`C:\Program Files\deja\deja.exe`,
		`C:\Users\a b\scoop\shims\deja.exe`,
	} {
		cmd := hookCommandQuoteFor("windows", path)
		out, err := exec.Command("bash", "-c", "printf %s "+cmd).Output()
		if err != nil {
			t.Fatalf("bash -c %q: %v", cmd, err)
		}
		if want := strings.ReplaceAll(path, `\`, "/"); string(out) != want {
			t.Errorf("wrote %q, bash ran %q, want %q", cmd, out, want)
		}
	}
}
