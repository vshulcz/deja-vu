package main

import (
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Junie keeps its user-level wiring under $JUNIE_HOME, else ~/.junie
// (resolveJunieHomePath in the 3110.7 CLI; it reads user.home, not $HOME):
//
//	mcp/mcp.json   {"mcpServers": {name: {command, args, env}}}
//	config.json    {"hooks": {Event: [{matcher, hooks: [{type, command, timeout}]}]}}
//	AGENTS.md      in front of every task (rules.go)
//	commands/*.md  the /name commands of the TUI
//
// A project's .junie/ holds the same files and is read only once the person
// trusts the project; the user-level ones are always read.
//
// Hooks follow Claude Code's schema. Of the events, two put text in front of
// the model, measured on a 3110.7 stand against a stub endpoint:
// UserPromptSubmit and PreToolUse, whose stdout, plain or as
// hookSpecificOutput.additionalContext, Junie wraps in <additional_context>
// before the prompt or the tool's result. SessionStart's is not delivered, and
// Stop's blocks the task from finishing, so neither is wired. There is no
// PostToolUse and no PreCompact: the --junie wrapper (hook_junie.go) reads a
// failed command and a compaction out of events.jsonl at the next hook.

func junieMCPPath() string {
	return filepath.Join(sources.JunieHome(), "mcp", "mcp.json")
}

func junieConfigPath() string {
	return filepath.Join(sources.JunieHome(), "config.json")
}

// junieHookWiring is every hook deja installs into Junie. PreToolUse also
// fires for Junie's own tools (submit, among others), and its context then
// shows in the task's result, so the matcher keeps to the three a line is for.
var junieHookWiring = []struct {
	Event, Matcher string
	Args           []string
}{
	{"UserPromptSubmit", "", []string{"hook-context", "--plain", "--once", "--junie"}},
	{"UserPromptSubmit", "", []string{"hook-prompt", "--plain", "--junie"}},
	{"PreToolUse", "Bash|Read|Edit", []string{"hook-tool", "--plain", "--junie"}},
	{"SessionEnd", "", []string{"hook-session-end"}},
}

// junieHookTimeout is in seconds.
const junieHookTimeout = 30

func installJunie(exe string, uninstall bool) (installResult, error) {
	return installMCPJSON(junieMCPPath(), exe, uninstall)
}

func installJunieHooks(exe string, uninstall bool) (installResult, error) {
	exe = hookExeFor(exe, uninstall)
	var res installResult
	for i, h := range junieHookWiring {
		r, err := installSettingsHookCmd(junieConfigPath(), h.Event, h.Matcher, junieHookTimeout, hookRun(exe, h.Args...), uninstall)
		if err != nil {
			return installResult{}, err
		}
		if i == 0 || (res.Action == "unchanged" && r.Action != "unchanged") {
			res = r
		}
	}
	return res, nil
}

// installJunieAuto writes the hooks first: a config file deja refuses should
// leave nothing half-wired (#2745).
func installJunieAuto(exe string, uninstall bool) (installResult, error) {
	hooks, err := installJunieHooks(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	mcp, err := installJunie(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	out := wroteAll(hooks, mcp)
	if !uninstall {
		out.Note = joinNotes(out.Note, "a project's own .junie/config.json hooks run beside these once the project is trusted")
	}
	return out, nil
}

// jetBrainsMCPPath is AI Assistant's global MCP file: ~/.ai/mcp/mcp.json, the
// default of the llm.mcp.client.global.mcp.json.path registry key
// (McpApplicationServerConfigurationService, plugin 262.10968.170), in the
// same {"mcpServers": …} shape. Every JetBrains IDE on the machine reads it.
func jetBrainsMCPPath() string {
	return filepath.Join(homeDir(), ".ai", "mcp", "mcp.json")
}

func installJetBrains(exe string, uninstall bool) (installResult, error) {
	return installMCPJSON(jetBrainsMCPPath(), exe, uninstall)
}
