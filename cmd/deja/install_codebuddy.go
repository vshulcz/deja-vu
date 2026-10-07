package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// CodeBuddy Code takes MCP servers and hooks the way Claude Code does, in its
// own files.
//
// MCP: user-scope servers live in the first of <config>/.mcp.json,
// <config>/mcp.json and <base>/.codebuddy.json that exists, else the first,
// where <config> is $CODEBUDDY_CONFIG_DIR or ~/.codebuddy and <base> is
// $CODEBUDDY_CONFIG_DIR or the home directory (PathUtils.getMcpCandidatePaths
// and resolveMcpFilePath in @tencent-ai/codebuddy-code 2.161.2, and the CLI's
// MCP doc). Only that one file is read, so deja writes into it rather than a
// new one the existing servers would shadow. An entry with a command and no
// type is taken as stdio.
//
// Hooks: <config>/settings.json, {"hooks":{Event:[{matcher, hooks:[{type,
// command, timeout}]}]}}, timeout in seconds. UserPromptSubmit and
// SessionStart take hookSpecificOutput.additionalContext; PostToolUse and
// PostToolUseFailure take it too when hookEventName names the event, and put
// it beside the tool result — all as Claude Code does, which is the shape
// deja's hooks already answer in.

func codeBuddyMCPPath() string {
	base := homeDir()
	if v := strings.TrimSpace(os.Getenv("CODEBUDDY_CONFIG_DIR")); v != "" {
		base = v
	}
	return codeBuddyMCPPathIn(sources.CodeBuddyConfigDir(), base, ".mcp.json")
}

// codeBuddyMCPPathIn is the first of the files the agent reads that exists,
// else <config>/<create>.
func codeBuddyMCPPathIn(cfg, base, create string) string {
	candidates := []string{
		filepath.Join(cfg, ".mcp.json"),
		filepath.Join(cfg, "mcp.json"),
		filepath.Join(base, ".codebuddy.json"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join(cfg, create)
}

func codeBuddySettingsPath() string {
	return filepath.Join(sources.CodeBuddyConfigDir(), "settings.json")
}

// WorkBuddy runs the same agent with CODEBUDDY_CONFIG_DIR set to its own home
// (buildAgentCliRuntimeEnv in the app), so its wiring is CodeBuddy's in that
// directory. A new MCP file is the app's own mcp.json, the one its server
// settings write: the agent reads only the first of .mcp.json and mcp.json,
// and a .mcp.json from deja would hide every server added in the app.
func workBuddyMCPPath() string {
	cfg := sources.WorkBuddyConfigDir()
	return codeBuddyMCPPathIn(cfg, cfg, "mcp.json")
}

func workBuddySettingsPath() string {
	return filepath.Join(sources.WorkBuddyConfigDir(), "settings.json")
}

// codeBuddyHookWiring is every event deja installs into CodeBuddy. Bash and
// PowerShell are CodeBuddy's shell tools by those names. PreToolUse is left
// out: what CodeBuddy does with a PreToolUse hook's context is not checked.
var codeBuddyHookWiring = []struct{ Event, Sub, Matcher string }{
	{"SessionStart", "hook-context", ""},
	{"UserPromptSubmit", "hook-prompt", ""},
	{"PostToolUse", "hook-tool-after", "Bash|PowerShell"},
	{"PostToolUseFailure", "hook-tool-after", "Bash|PowerShell"},
	// The file line, after a read and after an edit, on the channel this
	// file says is checked.
	{"PostToolUse", "hook-tool", "Read|Edit|Write|MultiEdit"},
	{"PreCompact", "hook-precompact", ""},
	{"SessionEnd", "hook-session-end", ""},
}

// codeBuddyHookTimeout is in seconds, CodeBuddy's default.
const codeBuddyHookTimeout = 60

func installCodeBuddyMCP(exe string, uninstall bool) (installResult, error) {
	return installMCPJSON(codeBuddyMCPPath(), exe, uninstall)
}

func installCodeBuddyHooksIn(path, exe string, uninstall bool) (installResult, error) {
	exe = hookExeFor(exe, uninstall)
	var res installResult
	for i, h := range codeBuddyHookWiring {
		r, err := installSettingsHookCmd(path, h.Event, h.Matcher, codeBuddyHookTimeout, codeBuddyHookRun(runtime.GOOS, exe, h.Sub), uninstall)
		if err != nil {
			return installResult{}, err
		}
		if i == 0 || (res.Action == "unchanged" && r.Action != "unchanged") {
			res = r
		}
	}
	return res, nil
}

// codeBuddyHookRun is the hook line for CodeBuddy. On Windows CodeBuddy hands
// a hook to Git Bash when it finds one and to PowerShell -Command otherwise,
// and PowerShell reads `"C:/First Last/deja.exe" hook-prompt` as a string with
// a stray token after it, so every hook failed (#4728). A line whose first
// word is powershell is spawned directly instead, whichever shell is there
// (tryBuildDirectPowerShellHookCommand, 2.161), and the inner command runs the
// quoted path behind `&`. Older builds hand the same line to bash or to
// PowerShell, and both run it as well.
//
// Only for a path that needs quoting: a plain one already runs in both shells
// and costs no extra process. A `$` or backtick would be expanded inside the
// double quotes by bash or PowerShell, so such a path keeps the old line.
//
// PowerShell exits 1 for any failed native command, so the line passes deja's
// own exit code on. It is read with Get-Variable because `$LASTEXITCODE`
// would be expanded by bash or an outer PowerShell before the inner one ran.
func codeBuddyHookRun(goos, exe, sub string) string {
	p := strings.ReplaceAll(exe, `\`, "/")
	if goos != "windows" || !strings.ContainsAny(p, " \t") || strings.ContainsAny(p, "$`") {
		return hookCommandQuoteFor(goos, exe) + " " + sub
	}
	return powerShellHookHead + "& '" + strings.ReplaceAll(p, "'", "''") + "' " + sub + powerShellHookTail
}

// installCodeBuddyAuto writes the hooks first: a settings file deja refuses
// should leave nothing half-wired (#2745).
func installCodeBuddyAuto(exe string, uninstall bool) (installResult, error) {
	r, err := installCodeBuddyAutoIn(codeBuddySettingsPath(), codeBuddyMCPPath(), exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	status, err := installCodeBuddyStatusline(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(r, status), nil
}

func installWorkBuddyAuto(exe string, uninstall bool) (installResult, error) {
	return installCodeBuddyAutoIn(workBuddySettingsPath(), workBuddyMCPPath(), exe, uninstall)
}

func installCodeBuddyAutoIn(settings, mcpPath, exe string, uninstall bool) (installResult, error) {
	hooks, err := installCodeBuddyHooksIn(settings, exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	mcp, err := installMCPJSON(mcpPath, exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(hooks, mcp), nil
}
