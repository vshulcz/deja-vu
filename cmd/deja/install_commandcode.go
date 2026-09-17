package main

import (
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Command Code keeps every user-scoped surface under `~/.commandcode`, and all
// four of them are shapes deja already writes:
//
//	mcp.json                        the server, `mcpServers`
//	settings.json                   hooks, under a `hooks` key in Claude's shape
//	skills/<name>/SKILL.md          Anthropic-style skills
//	commands/<name>.md              slash commands, name from the basename
//
// The CLI is closed, so none of this was read out of its source. Two
// independent integrations agree on it and both cite the vendor's own docs:
// rulesync's `src/constants/commandcode-paths.ts` and
// `src/types/hooks.ts` (four documented events — PreToolUse, PostToolUse,
// Stop, SessionStart — `command` hooks only, timeout in seconds), and
// akitaonrails/ai-memory's support matrix, which reports the same MCP file and
// says its SessionStart injects. Nothing here is verified on the machine deja
// was written on, and the registry entry says so (#3651).
func commandCodeConfigDir() string {
	return filepath.Join(homeDir(), ".commandcode")
}

func commandCodeMCPPath() string  { return filepath.Join(commandCodeConfigDir(), "mcp.json") }
func commandCodeSettings() string { return filepath.Join(commandCodeConfigDir(), "settings.json") }

func commandCodeSkillPath() string {
	return filepath.Join(commandCodeConfigDir(), "skills", "deja-search", "SKILL.md")
}

// commandCodeHookWiring is what deja has to say at each of the events Command
// Code documents. There is no per-prompt event — the four are PreToolUse,
// PostToolUse, Stop and SessionStart — so the digest rides SessionStart and
// the rest is the tool-time pair.
//
// The matcher is a regex over the tool's display name, and those names are its
// own: SHELL, READ, EDIT, WRITE, SEARCH, GLOB, LIST. A Claude-shaped `Bash`
// matcher never fires here, which is the trap the same adapter warns about.
var commandCodeHookWiring = []struct{ Event, Sub, Matcher string }{
	{"SessionStart", "hook-context", ""},
	{"PreToolUse", "hook-tool", "SHELL|EDIT|WRITE"},
	{"PostToolUse", "hook-tool-after", "SHELL"},
}

func installCommandCode(exe string, uninstall bool) (installResult, error) {
	server, err := installMCPJSON(commandCodeMCPPath(), exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	skill, err := installSkillFile(commandCodeSkillPath(), uninstall)
	if err != nil {
		return installResult{}, err
	}
	command, err := installCommandFile("commandcode", exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(server, skill, command), nil
}

func installCommandCodeAuto(exe string, uninstall bool) (installResult, error) {
	// The server and the rest first: `deja install --auto` installs the -auto
	// target alone, and hooks without a server would leave the tool half-wired.
	base, err := installCommandCode(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	hookExe := hookExeFor(exe, uninstall)
	results := []installResult{base}
	for _, h := range commandCodeHookWiring {
		// Seconds here, not milliseconds: Command Code's timeout unit is
		// seconds (default 30, max 600), so the numbers the other
		// settings.json targets pass would be ten minutes each.
		r, err := installSettingsHookCmd(commandCodeSettings(), h.Event, h.Matcher, 30,
			hookRun(hookExe, h.Sub), uninstall)
		if err != nil {
			return installResult{}, err
		}
		results = append(results, r)
	}
	return wroteAll(results...), nil
}

// commandCodeFirstRoot is what says Command Code is on this machine: its own
// project store, which only exists once it has run.
func commandCodeFirstRoot() string { return sources.CommandCodeRoot() }
