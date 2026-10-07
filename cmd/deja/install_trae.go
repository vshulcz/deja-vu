package main

import (
	"fmt"
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// TRAE CLI 2.0 is a codex-rs fork and is wired the way Codex is, in its own
// files. Each shape below was read off traecli 0.207.1 itself:
//
// MCP: `traex mcp add deja -- <deja> mcp` appends [mcp_servers.deja] with
// command and args, no type, to ${TRAE_HOME:-~/.trae}/traecli.toml, and
// `traex mcp list` shows it enabled. TRAECLI_HOME does not move this file:
// with it set, `traex mcp add` still wrote to $TRAE_HOME/traecli.toml.
//
// Hooks: ${TRAECLI_HOME:-$TRAE_HOME/cli}/hooks.json, the file `traex migrate
// hooks --user` copies a legacy ~/.trae/hooks.json to. The binary carries
// Codex's events, payload and hookSpecificOutput.additionalContext, and its
// trust store: hooks.state tables with a trusted_hash, kept in traecli.toml.
// A new or changed hook runs only after the user trusts it at start-up.
//
// Skill: `traex debug prompt-input` lists a skill placed only in
// ~/.agents/skills, so the shared skill reaches it (sharedSkillHarnesses).

func traeConfigPath() string { return filepath.Join(sources.TraeHome(), "traecli.toml") }

func traeHooksPath() string { return filepath.Join(sources.TraeCLIHome(), "hooks.json") }

func installTrae(exe string, uninstall bool) (installResult, error) {
	cmd, args := mcpCommandArgs(exe)
	block := fmt.Sprintf("[mcp_servers.deja]\ncommand = %q\nargs = %s\n", cmd, tomlStringArray(args))
	return installTOML(traeConfigPath(), block, uninstall)
}

// traeHookWiring is Codex's, with the pre-edit hook also on Edit and Write:
// traecli 0.208 edits through Claude Code's tools, not apply_patch, and sends
// PreToolUse with tool_name "Edit" and tool_input {"command": "Edit <path>"}.
// A command that exits non-zero fires PostToolUseFailure and not PostToolUse
// there, so the fix pair is wired on both.
func traeHookWiring() []hookWire {
	w := append([]hookWire(nil), codexHookWiring...)
	for i := range w {
		if w[i].Event == "PreToolUse" {
			w[i].Matcher = "Bash|apply_patch|Edit|Write"
		}
	}
	return append(w, hookWire{"PostToolUseFailure", "hook-tool-after", "Bash"})
}

// installTraeAuto writes the hooks first: a hooks.json deja refuses should
// leave nothing half-wired (#2745).
func installTraeAuto(exe string, uninstall bool) (installResult, error) {
	hooks, err := installCodexHooksAt(exe, traeHooksPath(), traeConfigPath(), traeHookWiring(), uninstall)
	if err != nil {
		return installResult{}, err
	}
	mcp, err := installTrae(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	status, err := installTraeStatusline(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	if !uninstall && hooks.Action != "unchanged" {
		fmt.Println("trae: open traex once and trust the hooks it shows — until then it runs none of them")
	}
	return wroteAll(hooks, mcp, status), nil
}
