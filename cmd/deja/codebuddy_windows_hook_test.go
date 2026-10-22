package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func codebuddyHookCommand(t *testing.T, entry any) any {
	t.Helper()
	group, _ := entry.(map[string]any)
	hooks, _ := group["hooks"].([]any)
	if len(hooks) == 0 {
		t.Fatalf("no hooks in %v", entry)
	}
	h, ok := hooks[0].(map[string]any)
	if !ok {
		t.Fatalf("hook is not an object: %v", hooks[0])
	}
	return h["command"]
}

// hookEchoEnv turns the test binary into a stand-in for deja: it prints its
// arguments on one line and then whatever came in on stdin, and exits 0, or
// with the code in hookEchoExitEnv.
const (
	hookEchoEnv     = "DEJA_TEST_HOOK_ECHO"
	hookEchoExitEnv = "DEJA_TEST_HOOK_EXIT"
)

func hookEcho() {
	in, _ := io.ReadAll(os.Stdin)
	_, _ = os.Stdout.WriteString(strings.Join(os.Args[1:], " ") + "\n")
	_, _ = os.Stdout.Write(in)
	code, _ := strconv.Atoi(os.Getenv(hookEchoExitEnv))
	os.Exit(code)
}

// cbParseCommandLine is CodeBuddy's parseCommandLine (2.161.3): split on
// blanks, double quotes group, and `\"` or "`\"" inside them is a quote.
func cbParseCommandLine(s string) (string, []string, error) {
	var out []string
	var cur strings.Builder
	inQ, have := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQ {
			if (c == '`' || c == '\\') && i+1 < len(s) && s[i+1] == '"' {
				cur.WriteByte('"')
				i++
				continue
			}
			if c == '"' {
				inQ = false
				continue
			}
			cur.WriteByte(c)
			continue
		}
		switch c {
		case '"':
			inQ, have = true, true
		case ' ', '\t':
			if have {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteByte(c)
			have = true
		}
	}
	if inQ {
		return "", nil, errors.New("unterminated quote")
	}
	if have {
		out = append(out, cur.String())
	}
	if len(out) == 0 {
		return "", nil, errors.New("empty")
	}
	return out[0], out[1:], nil
}

// cbIsPowerShell is CodeBuddy's isPowerShellExecutable.
func cbIsPowerShell(exe string) bool {
	if i := strings.LastIndexAny(exe, `/\`); i >= 0 {
		exe = exe[i+1:]
	}
	switch strings.ToLower(exe) {
	case "powershell", "powershell.exe", "pwsh", "pwsh.exe":
		return true
	}
	return false
}

// The line deja writes for CodeBuddy on Windows when the path has a space, and
// how CodeBuddy's hook executor takes it apart: powershell as the program, and
// the inner command as one argument. Elsewhere, and for a plain path, the line
// is the one every other harness gets (#4728).
func TestCodeBuddyWindowsHookLineForAPathWithASpace(t *testing.T) {
	exe := `C:\Users\First Last\AppData\Local\deja\deja.exe`
	line := codeBuddyHookRun("windows", exe, "hook-context")
	want := `powershell -NoProfile -Command "& 'C:/Users/First Last/AppData/Local/deja/deja.exe' hook-context; exit (Get-Variable LASTEXITCODE -ValueOnly)"`
	if line != want {
		t.Fatalf("line\n got %s\nwant %s", line, want)
	}
	prog, args, err := cbParseCommandLine(line)
	if err != nil || !cbIsPowerShell(prog) {
		t.Fatalf("CodeBuddy would not spawn this directly: %q %q %v", prog, args, err)
	}
	if got := args[len(args)-1]; got != `& 'C:/Users/First Last/AppData/Local/deja/deja.exe' hook-context; exit (Get-Variable LASTEXITCODE -ValueOnly)` {
		t.Errorf("the command PowerShell gets: %q", got)
	}

	if got := codeBuddyHookRun("windows", `C:\Users\O'Brien Smith\deja.exe`, "hook-prompt"); !strings.Contains(got, `'C:/Users/O''Brien Smith/deja.exe'`) {
		t.Errorf("a quote in the path is not doubled for PowerShell: %s", got)
	}
	for exe, want := range map[string]string{
		`C:\Users\me\deja.exe`:     `C:/Users/me/deja.exe hook-prompt`,
		`C:\Users\a $b\deja.exe`:   `"C:/Users/a $b/deja.exe" hook-prompt`,
		`/Users/A B/bin/deja`:      hookCommandQuoteFor("darwin", `/Users/A B/bin/deja`) + " hook-prompt",
		`/usr/local/bin/deja-hook`: `/usr/local/bin/deja-hook hook-prompt`,
	} {
		goos := "windows"
		if strings.HasPrefix(exe, "/") {
			goos = "darwin"
		}
		if got := codeBuddyHookRun(goos, exe, "hook-prompt"); got != want {
			t.Errorf("%s on %s: got %s, want %s", exe, goos, got, want)
		}
	}
	if runtime.GOOS != "windows" {
		if got, want := codeBuddyHookRun(runtime.GOOS, "/Users/A B/bin/deja", "hook-prompt"), hookRun("/Users/A B/bin/deja", "hook-prompt"); got != want {
			t.Errorf("the %s line changed: %s, was %s", runtime.GOOS, got, want)
		}
	}
}

// An entry written before as `"C:/…/deja.exe" hook-context` is deja's own: an
// install takes it over with the new line instead of adding a second hook
// beside it, and an uninstall takes the new line out.
func TestCodeBuddyWindowsHookLineReplacesTheQuotedOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	exe := `C:\Users\First Last\AppData\Local\deja\deja.exe`
	old := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"\"C:/Users/First Last/AppData/Local/deja/deja.exe\" hook-context","timeout":60}]}]}}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	line := codeBuddyHookRun("windows", exe, "hook-context")
	if _, err := installSettingsHookCmd(path, "SessionStart", "", 60, line, false); err != nil {
		t.Fatal(err)
	}
	cfg := readJSONConfig(path)
	entries, _ := cfg["hooks"].(map[string]any)["SessionStart"].([]any)
	if len(entries) != 1 {
		t.Fatalf("%d SessionStart entries, want 1: %v", len(entries), entries)
	}
	if got := codebuddyHookCommand(t, entries[0]); got != line {
		t.Fatalf("command %q, want %q", got, line)
	}
	// From another install path: still ours, still one entry.
	moved := codeBuddyHookRun("windows", `D:\Tools Dir\deja.exe`, "hook-context")
	if _, err := installSettingsHookCmd(path, "SessionStart", "", 60, moved, false); err != nil {
		t.Fatal(err)
	}
	entries, _ = readJSONConfig(path)["hooks"].(map[string]any)["SessionStart"].([]any)
	if len(entries) != 1 || codebuddyHookCommand(t, entries[0]) != moved {
		t.Fatalf("after a move: %v", entries)
	}
	if _, err := installSettingsHookCmd(path, "SessionStart", "", 60, moved, true); err != nil {
		t.Fatal(err)
	}
	if hooks, _ := readJSONConfig(path)["hooks"].(map[string]any); len(hooks) != 0 {
		t.Fatalf("uninstall left %v", hooks)
	}
	// Somebody's own line that runs deja's hook among other things is still
	// theirs.
	wrapper := `powershell -NoProfile -Command "& 'C:/x y/deja.exe' hook-context; Write-Host done"`
	if kind := hookCommandKindOf(wrapper, line); kind != hookWrapsDejas {
		t.Errorf("a wrapper read as %v", kind)
	}
}

// What CodeBuddy actually does with the line on Windows, run for real against
// a stand-in deja whose path has a space: spawned directly when its first word
// is powershell (2.161), handed to PowerShell -Command when not and there is
// no Git Bash, and to Git Bash when there is one. The payload carries non-ASCII
// text both ways, as a prompt does. Before #4728 the first two failed with
// PowerShell's "Unexpected token 'hook-context'".
//
// And a failing deja: CodeBuddy acts on a hook's exit code, and PowerShell
// turns any failed native command into 1. Where CodeBuddy spawns the line, or
// Git Bash runs it, the code deja exited with is the one it sees. An older
// CodeBuddy that wraps the line in a second PowerShell flattens it again, out
// of deja's reach; there it only has to stay a failure.
func TestCodeBuddyHookLineRunsInEveryWindowsShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("CodeBuddy's Windows hook executor")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "First Last")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "deja.exe")
	copyFile(t, self, exe)

	sub := "hook-context"
	line := codeBuddyHookRun(runtime.GOOS, exe, sub)
	payload := `{"prompt":"修复 the build — café","hook_event_name":"SessionStart"}`
	want := sub + "\n" + payload

	type run struct {
		name  string
		argv  []string
		exact bool // the exit code passes through unchanged
	}
	var runs []run
	// CodeBuddy 2.161: tryBuildDirectPowerShellHookCommand, else the shell.
	if prog, args, err := cbParseCommandLine(line); err == nil && cbIsPowerShell(prog) {
		args = append(append([]string{}, args[:len(args)-2]...), append([]string{"-NonInteractive", "-WindowStyle", "Hidden"}, args[len(args)-2:]...)...)
		runs = append(runs, run{"CodeBuddy, spawned directly", append([]string{prog}, args...), true})
	} else {
		runs = append(runs, run{"CodeBuddy, no Git Bash", []string{"powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", line}, true})
	}
	// An older CodeBuddy without the direct spawn, and no Git Bash.
	runs = append(runs, run{"PowerShell -Command", []string{"powershell", "-NoProfile", "-NonInteractive", "-Command", line}, false})
	if _, err := exec.LookPath("pwsh"); err == nil {
		runs = append(runs, run{"pwsh -Command", []string{"pwsh", "-NoProfile", "-NonInteractive", "-Command", line}, false})
	}
	if bash := gitBashPath(); bash != "" {
		runs = append(runs, run{"Git Bash", []string{bash, "-c", line}, true})
	} else {
		t.Log("no Git Bash on this machine; the bash leg is not run")
	}
	for _, r := range runs {
		c := exec.CommandContext(t.Context(), r.argv[0], r.argv[1:]...)
		c.Env = append(os.Environ(), hookEchoEnv+"=1")
		c.Stdin = strings.NewReader(payload)
		var stderr strings.Builder
		c.Stderr = &stderr
		out, err := c.Output()
		got := strings.ReplaceAll(string(out), "\r\n", "\n")
		if err != nil || got != want {
			t.Errorf("%s ran %q:\n err %v\n out %q\nwant %q\n stderr %s", r.name, line, err, got, want, stderr.String())
		}

		c = exec.CommandContext(t.Context(), r.argv[0], r.argv[1:]...)
		c.Env = append(os.Environ(), hookEchoEnv+"=1", hookEchoExitEnv+"=3")
		c.Stdin = strings.NewReader(payload)
		err = c.Run()
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			t.Errorf("%s: %v", r.name, err)
			continue
		}
		if r.exact && code != 3 {
			t.Errorf("%s: deja exited 3, CodeBuddy sees %d", r.name, code)
		} else if code == 0 {
			t.Errorf("%s: deja exited 3, CodeBuddy sees success", r.name)
		}
	}
}

// gitBashPath is Git for Windows' bash, which is what CodeBuddy looks for; the
// bash.exe in System32 is WSL's and is not it.
func gitBashPath() string {
	for _, p := range []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "bash.exe"),
		filepath.Join(os.Getenv("ProgramW6432"), "Git", "bin", "bash.exe"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o755); err != nil {
		t.Fatal(err)
	}
}
