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
func grokHooksPath() string {
	return filepath.Join(sources.GrokHome(), "hooks", "deja.json")
}

func installGrokAuto(exe string, uninstall bool) (installResult, error) {
	path := grokHooksPath()
	if uninstall {
		if _, err := os.Stat(path); err != nil {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		if err := os.Remove(path); err != nil {
			return installResult{}, err
		}
		return installResult{Path: path, Action: "removed"}, nil
	}
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return installResult{}, err
	}
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return installResult{}, configParseError(path, err)
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
	root = updateClaudeHook(root, "SessionStart", exe+" hook-context", "", false)
	root = updateClaudeHook(root, "PreCompact", exe+" hook-precompact", "", false)
	root = updateClaudeHook(root, "UserPromptSubmit", exe+" hook-prompt", "", false)
	// Scoped to the tools that change something, so it never fires on a read.
	// Grok maps the Claude names onto its own, so `Bash` here reaches
	// run_terminal_command, `Write` reaches write and `Agent` reaches
	// spawn_subagent — the one of them whose reply grok acts on.
	root = updateClaudeHook(root, "PreToolUse", exe+" hook-tool", "Bash|Edit|Write|MultiEdit|NotebookEdit|Task|Agent", false)
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return installResult{}, err
	}
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}
