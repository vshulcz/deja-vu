package main

import (
	"bytes"
	"encoding/json"
	"os"
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
	old, _ := os.ReadFile(path)
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
		if entry != nil && entry["command"] == cmd {
			found = true
			if uninstall {
				continue
			}
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
