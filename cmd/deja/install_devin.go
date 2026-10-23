package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

// Devin CLI reads the same two config surfaces as every other agent: an MCP
// server block and a hooks object. Its user-level MCP file is
// ~/.config/devin/mcp_config.json (%APPDATA%\devin\mcp_config.json on
// Windows), and its hooks live under the "hooks" key of
// ~/.config/devin/config.json — the same {event: [{matcher, hooks:
// [{type:"command",command}]}]} shape Claude Code's settings carry, so the
// writer Claude's install uses maintains it.
//
// What Devin fires is Claude's own vocabulary — SessionStart,
// UserPromptSubmit, PreToolUse, PostToolUse, SessionEnd, PostCompaction —
// and every payload names its event under hook_event_name, which is why the
// hook commands need no Devin-specific variants: the reply is emitted under
// the event it arrived on, so PostToolUse context lands where PreToolUse
// context cannot (Devin drops additionalContext on PreToolUse — verified on
// 3000.11.3).

// devinConfigDir is Devin CLI's user config directory, the platform's own:
// ~/.config/devin on Linux and macOS (XDG_CONFIG_HOME honoured),
// %APPDATA%\devin on Windows.
func devinConfigDir() string { return devinConfigDirFor(runtime.GOOS) }

// devinConfigDirFor is devinConfigDir with the platform spelled out, so the
// Windows branch is covered from a Linux test the same way
// hookCommandQuoteFor's is.
func devinConfigDirFor(goos string) string {
	if goos == "windows" {
		app := os.Getenv("APPDATA")
		if app == "" {
			app = filepath.Join(homeDir(), "AppData", "Roaming")
		}
		return filepath.Join(app, "devin")
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(homeDir(), ".config")
	}
	return filepath.Join(cfg, "devin")
}

// devinMCPConfigPath is the user-scope MCP file `devin mcp add --scope user`
// writes.
func devinMCPConfigPath() string {
	return filepath.Join(devinConfigDir(), "mcp_config.json")
}

// devinConfigPath is the user config whose "hooks" key carries the wiring.
func devinConfigPath() string {
	return filepath.Join(devinConfigDir(), "config.json")
}

// devinHookWiring is the hook layer `deja install devin-auto` writes, in the
// shape updateClaudeHook maintains: event, the deja hook's args, and the
// tool-name matcher.
var devinHookWiring = []struct{ Event, Sub, Matcher string }{
	{"SessionStart", "hook-context", ""},
	// After a compaction the model just lost its working context — the same
	// reason Claude's SessionStart source=compact gets a lead line. Devin's
	// own docs name re-injecting context as this event's use; the payload
	// names PostCompaction, which is the name the reply goes out under.
	{"PostCompaction", "hook-context", ""},
	{"UserPromptSubmit", "hook-prompt", ""},
	// Claude wires this on PreToolUse, where deja names the file's or
	// command's prior decision before the action. Devin's PreToolUse reads a
	// decision, not additionalContext — the field is accepted and dropped
	// (verified on 3000.11.3) — so the line rides on PostToolUse instead:
	// late for the action that fired it, in time for everything the agent
	// does next on that file. exec runs commands; the file tools are edit,
	// write, apply_patch and notebook_edit. run_subagent is absent on
	// purpose: other hosts get the spawn brief before the child starts, and
	// a PostToolUse line lands only after it is already running.
	{"PostToolUse", "hook-tool", "^(exec|edit|write|apply_patch|notebook_edit)$"},
	// The fix-pair line, on the shell: a failed exec carries the error its
	// output names, and the store knows what followed that error before.
	// Devin has no PostToolUseFailure — a nonzero exit is an ordinary
	// PostToolUse with success:false — so one wiring covers both.
	{"PostToolUse", "hook-tool-after", "^exec$"},
	// The session is over, so its live stamp goes and the next session's
	// recall can answer with it.
	{"SessionEnd", "hook-session-end", ""},
}

// installDevinMCP is the `devin` target: the MCP server alone.
func installDevinMCP(exe string, uninstall bool) (installResult, error) {
	mcp, err := installMCPJSON(devinMCPConfigPath(), exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	skill, err := installSkillFile(sharedSkillPath(), uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(mcp, skill), nil
}

// installDevinAuto is `devin-auto`: the hook layer plus the MCP server.
func installDevinAuto(exe string, uninstall bool) (installResult, error) {
	hooks, err := installDevinHooks(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	mcp, err := installDevinMCP(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(hooks, mcp), nil
}

// installDevinHooks maintains the "hooks" object of ~/.config/devin/
// config.json through Claude's writer: the entry shape is identical
// ({matcher, hooks:[{type:"command",command}]}), so every self-heal that
// writer learned applies here too.
func installDevinHooks(exe string, uninstall bool) (installResult, error) {
	exe = hookExeFor(exe, uninstall)
	path := devinConfigPath()
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if uerr := json.Unmarshal(old, &root); uerr != nil {
		return installResult{}, configParseError(path, uerr)
	}
	nextRoot := root
	for _, h := range devinHookWiring {
		nextRoot = updateClaudeHook(nextRoot, h.Event, hookRun(exe, h.Sub), h.Matcher, uninstall)
	}
	next, err := marshalConfigLike(old, nextRoot)
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}
