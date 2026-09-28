package main

import (
	"os"
	"os/exec"
	"runtime"
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

// The same written command, run for real by the other two shells a Windows
// harness may hand it to. The test binary is the executable: asked to run no
// tests, it exits 0, which is all a hook host sees.
func TestAWindowsHookPathRunsInCmdAndPowerShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows shells")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := hookCommandQuote(exe) + " -test.run=NoSuchTest"
	if strings.Contains(cmd, `"`) {
		// PowerShell reads a leading quoted string as a value, not a command;
		// that is true of the quoting before this change as well.
		t.Skipf("the temp path has a space: %s", exe)
	}
	for _, sh := range [][]string{
		{"cmd", "/c", cmd},
		{"powershell", "-NoProfile", "-Command", cmd},
	} {
		if out, err := exec.Command(sh[0], sh[1:]...).CombinedOutput(); err != nil {
			t.Errorf("%s ran %q: %v\n%s", sh[0], cmd, err, out)
		}
	}
}
