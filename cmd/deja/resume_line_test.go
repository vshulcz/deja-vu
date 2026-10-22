package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The directory a resume line cds into is read off a session store, so it
// can hold anything a file name can. PowerShell, which the Windows line is
// pasted into, expands $(...) and backtick escapes inside the outer
// double-quoted -Command, ends it on U+201C/U+201D/U+201E as on ", and ends
// the inner single-quoted path on U+2018/U+2019/U+201A/U+201B as on '.
func TestWindowsResumeLineKeepsTheDirOneLiteral(t *testing.T) {
	const cmd = "claude --resume 019f"
	wrap := func(quoted string) string {
		return `powershell.exe -NoProfile -Command "Set-Location -LiteralPath ` + quoted + ` -ErrorAction Stop; ` + cmd + `"`
	}
	for _, c := range []struct {
		dir, want string
	}{
		{`C:\Users\bob's\app`, wrap(`'C:\Users\bob''s\app'`)},
		{"C:\\p\\a\u2018b", wrap("'C:\\p\\a\u2018\u2018b'")},
		{"C:\\p\\a\u2019b", wrap("'C:\\p\\a\u2019\u2019b'")},
		{"C:\\p\\a\u201ab", wrap("'C:\\p\\a\u201a\u201ab'")},
		{"C:\\p\\a\u201bb", wrap("'C:\\p\\a\u201b\u201bb'")},
		{`C:\p\50% a,b @x`, wrap(`'C:\p\50% a,b @x'`)},
	} {
		got, ok := resumeLine("windows", c.dir, cmd)
		if !ok || got != c.want {
			t.Errorf("resumeLine(windows, %q) = %q, %v\n want %q", c.dir, got, ok, c.want)
		}
	}
	for _, dir := range []string{
		`C:\p\a$(echo hi)b`,
		"C:\\p\\a`b",
		`C:\p\a"b`,
		"C:\\p\\a\u201cb",
		"C:\\p\\a\u201db",
		"C:\\p\\a\u201eb",
		"C:\\p\\a\x1b[2Jb",
		"C:\\p\\a\nb",
		// cmd.exe expands %name% even inside its double quotes, and !name!
		// under delayed expansion, before PowerShell sees the line: a user
		// named O'Brien turned %USERNAME% into a quote that ended the path.
		`C:\p\%USERNAME%;calc;#`,
		`C:\p\!USERNAME!;calc;#`,
	} {
		if got, ok := resumeLine("windows", dir, cmd); ok || got != cmd {
			t.Errorf("resumeLine(windows, %q) = %q, %v; want the command alone, no cd", dir, got, ok)
		}
	}
}

// fish reads \' and \\ inside single quotes as escapes where every other
// shell keeps them, so a single-quoted path with a backslash is a different
// word there: `a\'\';echo hi;#` ran `echo hi`.
func TestPosixResumeLineLeavesOutADirFishReadsDifferently(t *testing.T) {
	const cmd = "claude --resume 019f"
	got, ok := resumeLine("linux", "/tmp/bob's dir", cmd)
	if want := `cd '/tmp/bob'"'"'s dir' && ` + cmd; !ok || got != want {
		t.Errorf("resumeLine(linux, bob's dir) = %q, %v; want %q", got, ok, want)
	}
	for _, dir := range []string{`/tmp/a\'\';echo hi;#`, `/tmp/x\`, `/tmp/a\\b`, "/tmp/a\x1b[2Jb"} {
		if got, ok := resumeLine("linux", dir, cmd); ok || got != cmd {
			t.Errorf("resumeLine(linux, %q) = %q, %v; want the command alone, no cd", dir, got, ok)
		}
	}
}

// Whatever line is printed lands in the directory itself, in every shell on
// this machine that the line is meant for.
func TestResumeLineLandsInTheDirInEachShell(t *testing.T) {
	names := []string{"bob's dir", "a$(echo hi)b", "a`echo hi`b", "a;echo hi;b", "a\u2018b\u2019c", "50% a,b @x"}
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	var dirs []string
	for _, n := range names {
		d := filepath.Join(base, n)
		if err := os.Mkdir(d, 0o755); err != nil {
			continue // a name this file system refuses
		}
		dirs = append(dirs, d)
	}
	run := func(t *testing.T, goos, cmdline string, shell ...string) {
		t.Helper()
		for _, d := range dirs {
			line, ok := resumeLine(goos, d, cmdline)
			if !ok {
				continue
			}
			script, body := filepath.Join(t.TempDir(), "line"), line+"\n"
			if goos == "windows" {
				// Windows PowerShell reads a .ps1 without a BOM as ANSI, which
				// a line pasted at its prompt never goes through.
				script, body = script+".ps1", "\ufeff"+body
			}
			if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := exec.CommandContext(t.Context(), shell[0], append(shell[1:], script)...).CombinedOutput()
			got := strings.TrimSpace(string(out))
			if err != nil || got != d {
				t.Errorf("%s: %s\n printed %q, want %q (%v)", shell[0], line, got, d, err)
			}
		}
	}
	for _, sh := range []string{"sh", "bash", "zsh", "dash", "fish"} {
		if _, err := exec.LookPath(sh); err != nil || runtime.GOOS == "windows" {
			continue
		}
		t.Run(sh, func(t *testing.T) { run(t, "linux", "pwd", sh) })
	}
	// The Windows line runs powershell.exe inside whichever PowerShell it is
	// pasted into.
	if _, err := exec.LookPath("powershell.exe"); err == nil {
		for _, ps := range []string{"pwsh", "powershell.exe"} {
			if _, err := exec.LookPath(ps); err != nil {
				continue
			}
			t.Run(ps, func(t *testing.T) {
				run(t, "windows", "(Get-Location).ProviderPath", ps, "-NoProfile", "-NonInteractive", "-File")
			})
		}
	}
}
