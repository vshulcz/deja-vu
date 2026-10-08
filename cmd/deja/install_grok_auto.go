package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Grok reads hooks from ~/.grok/hooks/*.json in the same shape Claude Code
// uses, and its documented events are the same four deja already wires there.
// It also reads ~/.claude/settings.json, so a machine with Claude Code wired
// was getting grok's recall by accident; this makes it deliberate and works on
// a machine that has only grok.
//
// A file of deja's own rather than a shared settings file: the directory is
// scanned, so there is nothing to merge into and nothing of the user's to
// preserve.
//
// What grok does with a hook's answer is not what the other harnesses do, and
// it decides what is worth wiring here. Measured on 1.0.5 against a stubbed
// proxy, reading the request the model was actually sent: session start, the
// user prompt and both tool events are passive — whatever the hook prints is
// discarded, in `hookSpecificOutput.additionalContext`, as a flat
// `additionalContext`, and with the event name in either spelling. Two replies
// do reach the model: a PreToolUse `deny`, which deja never sends because it
// does not block work, and `Stop`, which reaches it by keeping the agent
// working for up to eight more rounds — a recall is not worth that.
//
// The exception is the one that matters. A PreToolUse reply carrying
// `updatedInput` is applied, and it is what puts memory inside a spawned
// agent's prompt (hook_spawn.go). So in grok the hooks below are wired for
// their side effects — warming the index, forgetting what a compaction threw
// away — and for the spawn, which is the one place deja still speaks.
//
// PostToolUse has changed since 1.0.5: grok 1.0.41's hook docs say its
// context goes to the model with the tool's result, so a failed command gets
// the fix pair there too (#4499). So has PreToolUse: on 1.0.41 its context
// reaches the model as a system reminder after the tool's result, which is
// where the file line arrives. Session start and the prompt have not: on
// 1.0.41 they still drop additionalContext, so under grok hook-context and
// hook-prompt park their answer for the next tool hook, which does reach the
// model (#4588, grokDropsContext, hook_deferred.go).
func grokHooksPath() string {
	return filepath.Join(sources.GrokHome(), "hooks", "deja.json")
}

func installGrokAuto(exe string, uninstall bool) (installResult, error) {
	exe = hookExeFor(exe, uninstall)
	path := grokHooksPath()
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		if uninstall {
			// A file deja cannot read is still one the uninstall has to take,
			// or grok keeps calling a binary wired nowhere else — the contract
			// TestUninstallStillTakesAHookFileTheInstallWouldRefuse pins.
			if rerr := os.Remove(path); rerr != nil {
				return installResult{}, rerr
			}
			pruneCreatedDir(filepath.Dir(path))
			return installResult{Path: path, Action: "removed"}, nil
		}
		return installResult{}, configParseError(path, err)
	}
	if uninstall {
		if len(old) == 0 {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		// Install merged deja's entries beside the reader's own; uninstall
		// removed the file whole and took theirs with it (#3219). deja's
		// entries come out, and the file goes only when nothing else is in it.
		for _, ev := range [][2]string{
			{"SessionStart", "hook-context"}, {"PreCompact", "hook-precompact"},
			{"UserPromptSubmit", "hook-prompt"}, {"PreToolUse", "hook-tool"},
			{"PostToolUse", "hook-tool-after"}, {"SessionEnd", "hook-session-end"},
		} {
			root = updateClaudeHook(root, ev[0], hookRun(exe, ev[1]), "", true)
		}
		if hooks, _ := root["hooks"].(map[string]any); len(hooks) == 0 {
			delete(root, "hooks")
		}
		if len(root) == 0 {
			if err := os.Remove(path); err != nil {
				return installResult{}, err
			}
			// And the hooks directory deja made for it (#3698).
			pruneCreatedDir(filepath.Dir(path))
			return installResult{Path: path, Action: "removed"}, nil
		}
		next, err := marshalConfigLike(old, root)
		if err != nil {
			return installResult{}, err
		}
		a, err := writeIfChanged(path, old, append(next, '\n'))
		return installResult{Path: path, Action: a}, err
	}
	// No matcher. Grok's own docs name the session sources `startup` and
	// `resume`, but 1.0.5 sends `new` for a fresh session and `load` for a
	// resumed one, so the matcher deja copied from Claude Code matched neither
	// and this hook had never once fired on grok. That is the expensive kind of
	// silence: hook-context is what starts the index warming, so grok was the
	// one harness where the first recall of a session paid for the rebuild
	// inline. An empty matcher takes whatever source grok names next, and
	// PreCompact drops its documented `manual|auto` for the same reason — every
	// trigger it can name is a compaction, which is the one deja wants.
	root = updateClaudeHook(root, "SessionStart", hookRun(exe, "hook-context"), "", false)
	root = updateClaudeHook(root, "PreCompact", hookRun(exe, "hook-precompact"), "", false)
	root = updateClaudeHook(root, "UserPromptSubmit", hookRun(exe, "hook-prompt"), "", false)
	// Grok maps the Claude names onto its own, so `Bash` here reaches
	// run_terminal_command, `Read` read_file, `Write` write and `Agent`
	// spawn_subagent. On 1.0.41 the context reaches the model too, as a
	// system reminder after the tool's result, so the file line goes out at
	// the read, the step before an edit, as well as at the edit.
	root = updateClaudeHook(root, "PreToolUse", hookRun(exe, "hook-tool"), "Bash|Read|Edit|Write|MultiEdit|NotebookEdit|Task|Agent", false)
	// Grok fires PostToolUse for a run_terminal_command that exited non-zero
	// and hands the hook's context to the model with the result, so a failure
	// gets the earlier fix the way claude's does (#4499). PostToolUseFailure
	// is not the failure event here: grok fires it when a tool fails to
	// dispatch or an MCP tool errors, never for a command's exit code.
	root = updateClaudeHook(root, "PostToolUse", hookRun(exe, "hook-tool-after"), "Bash", false)
	// The session is over, so its live stamp goes: the next session's MCP
	// recall can answer with it now rather than twenty minutes from now. Grok
	// bounds a SessionEnd hook at 1.5 s by default, which this fits in.
	root = updateClaudeHook(root, "SessionEnd", hookRun(exe, "hook-session-end"), "", false)
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	noteCreatedDirs(filepath.Dir(path))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return installResult{}, err
	}
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}
