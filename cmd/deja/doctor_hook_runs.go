package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// claudeHookRunNote asks the shell Claude Code runs hooks in whether it can
// find the binary deja's hook names, spelled exactly as the settings file
// spells it. The binary being there is not enough: on Windows the path was
// written with backslashes, Git Bash stripped them, and every hook exited 127
// while doctor said "wired" (#4125, #4116).
//
// `command -v` goes through the same word splitting, quoting and escapes as
// running the command would, and runs nothing: executing deja from doctor
// would, among other things, start a test binary's whole suite under test.
func claudeHookRunNote(hooks map[string]any) string {
	exe := claudeHookExe(hooks)
	if exe == "" {
		return ""
	}
	sh := claudeHookShell()
	if sh == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, sh, "-c", "command -v "+exe).Run(); err == nil || ctx.Err() != nil {
		// A shell too slow to answer is not evidence the hook is broken.
		return ""
	} else if _, ok := err.(*exec.ExitError); !ok {
		return ""
	}
	// A file that is there and cannot run fails the same lookup; it exits 126,
	// and the remedy is the mode, not the path.
	// Windows has no exec bit to lose.
	if fi, err := os.Stat(strings.Trim(exe, `"'`)); err == nil && !fi.IsDir() && runtime.GOOS != "windows" && fi.Mode()&0o111 == 0 {
		return exe + " is not executable, so every hook exits 126 — `deja install claude-auto` restores it"
	}
	return filepath.Base(sh) + " cannot find " + exe + " as the hooks spell it, so every hook exits 127 — `deja install claude-auto` rewrites them"
}

// claudeHookExe is the part of deja's prompt hook in front of its subcommand:
// the binary or launcher, with whatever quoting the file gave it.
func claudeHookExe(hooks map[string]any) string {
	entries, _ := hooks["UserPromptSubmit"].([]any)
	for _, entryAny := range entries {
		entry, _ := entryAny.(map[string]any)
		hs, _ := entry["hooks"].([]any)
		for _, hAny := range hs {
			h, _ := hAny.(map[string]any)
			cmd, _ := h["command"].(string)
			if i := strings.Index(cmd, " hook-prompt"); i > 0 {
				if exe := strings.TrimSpace(cmd[:i]); hookExeIsOneWord(exe) {
					return exe
				}
			}
		}
	}
	return ""
}

// hookExeIsOneWord reports whether exe is a single word the way deja writes
// one: bare, or wholly inside one pair of quotes. Anything else is a command
// the reader built around deja — `cd x && deja`, `FOO=1 deja` — and handing
// that to `command -v` would run part of it.
func hookExeIsOneWord(exe string) bool {
	if n := len(exe); n >= 2 && (exe[0] == '"' || exe[0] == '\'') && exe[n-1] == exe[0] {
		inner := exe[1 : n-1]
		return !strings.ContainsAny(inner, "\"'$`")
	}
	return exe != "" && !strings.ContainsAny(exe, " \t\"'$`;&|<>(){}*?!=")
}

// claudeHookShell is the shell Claude Code hands hook commands to: Git Bash on
// Windows, found the way Claude Code finds it, and bash or sh elsewhere. The
// bash on a Windows PATH can be WSL's, which cannot run a Windows path at all,
// so it is not a stand-in — without Git Bash the check says nothing.
func claudeHookShell() string {
	if runtime.GOOS != "windows" {
		if p, err := exec.LookPath("bash"); err == nil {
			return p
		}
		if p, err := exec.LookPath("sh"); err == nil {
			return p
		}
		return ""
	}
	if p := os.Getenv("CLAUDE_CODE_GIT_BASH_PATH"); p != "" && fileExists(p) {
		return p
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	// git.exe sits in Git\cmd or Git\mingw64\bin; bash.exe in Git\bin.
	dir := filepath.Dir(git)
	for i := 0; i < 3 && dir != filepath.Dir(dir); i, dir = i+1, filepath.Dir(dir) {
		if p := filepath.Join(dir, "bin", "bash.exe"); fileExists(p) {
			return p
		}
	}
	return ""
}
