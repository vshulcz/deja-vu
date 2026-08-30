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
	root = updateClaudeHook(root, "SessionStart", exe+" hook-context", "startup|resume", false)
	root = updateClaudeHook(root, "PreCompact", exe+" hook-precompact", "manual|auto", false)
	root = updateClaudeHook(root, "UserPromptSubmit", exe+" hook-prompt", "", false)
	// Scoped to the tools that change something, so it never fires on a read.
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
