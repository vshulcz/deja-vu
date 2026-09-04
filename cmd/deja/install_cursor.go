package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Cursor's hook entries are flat: {"command": "..."} under an event key, and
// the hook prints {"additional_context": "..."} back. It also accepts Claude's
// nested hookSpecificOutput shape, which is what `deja hook-context` already
// emits — verified against cursor-agent 2026.07.17.
//
// Cursor reads ~/.claude/settings.json too and dedupes those hooks against its
// own by exact command string, so writing the same command here means a user
// with both installed still gets one injection, not two.
func installCursorHooks(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.CursorCLIHome(), "hooks.json")
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return installResult{}, configParseError(path, err)
	}
	if !uninstall {
		root["version"] = 1
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	// sessionStart carries the digest. beforeSubmitPrompt only fires in the
	// interactive TUI — headless `-p` skips it — but costs nothing there.
	setCursorHook(hooks, "sessionStart", exe+" hook-context", uninstall)
	setCursorHook(hooks, "beforeSubmitPrompt", exe+" hook-prompt", uninstall)
	// And the same three cursor already runs for anyone who also has Claude
	// Code: it reads ~/.claude/settings.json and maps the names onto its own —
	// PreToolUse→preToolUse, PostToolUse→postToolUse, PreCompact→preCompact
	// (the table is in its own bundle, 2026.07.23). A cursor-only user got none
	// of them. The dedupe is by exact command string, so a user with both still
	// gets one hook, not two.
	setCursorHook(hooks, "preToolUse", exe+" hook-tool", uninstall)
	setCursorHook(hooks, "postToolUse", exe+" hook-tool-after", uninstall)
	setCursorHook(hooks, "preCompact", exe+" hook-precompact", uninstall)
	if len(hooks) == 0 {
		delete(root, "hooks")
		delete(root, "version")
	}
	if len(root) == 0 && len(bytes.TrimSpace(old)) == 0 {
		return installResult{Path: path, Action: "unchanged"}, nil
	}
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

func setCursorHook(hooks map[string]any, event, cmd string, uninstall bool) {
	entries, _ := hooks[event].([]any)
	var kept []any
	found := false
	for _, entryAny := range entries {
		entry, _ := entryAny.(map[string]any)
		kind := hookNotDejas
		if entry != nil {
			kind = hookCommandKindOf(entry["command"], cmd)
		}
		// A wrapper the reader built around deja is theirs to keep — and it
		// already calls the hook, so writing deja's own line beside it would
		// run the same thing twice.
		if kind == hookWrapsDejas {
			found = true
			kept = append(kept, entryAny)
			continue
		}
		// An entry deja wrote from a path it no longer runs from is still
		// deja's. Comparing the whole command read it as a stranger's hook
		// worth keeping, so a move left cursor running the binary that is
		// gone alongside the one that is there, and appended one more entry
		// every time (#2691).
		if kind == hookDejas {
			if uninstall {
				found = true
				continue
			}
			// A file that already collected several of them converges here:
			// the first becomes this binary's line and the rest go, rather
			// than being rewritten into byte-identical copies of it.
			if found {
				continue
			}
			found = true
			entry["command"] = cmd
		}
		kept = append(kept, entryAny)
	}
	if !uninstall && !found {
		kept = append(kept, map[string]any{"command": cmd})
	}
	if len(kept) == 0 {
		delete(hooks, event)
		return
	}
	hooks[event] = kept
}
