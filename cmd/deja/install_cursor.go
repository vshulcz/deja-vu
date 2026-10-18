package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
	return cursorHooksUpdate(hookExeFor(exe, uninstall), uninstall, true)
}

// cursorHooksUpdate computes deja's entries in ~/.cursor/hooks.json and
// writes them when write is set. Unset, it is cursor-auto's pre-flight: the
// same refusals, nothing written, so a refused target leaves mcp.json as it
// was too (#2748).
//
// cursor-agent reads the file with its comments dropped (`parseJSONC` in its
// bundle, 2026.09.02: `//…` and `/*…*/` removed, then JSON.parse), so a
// commented file is a working one. A hook entry is an element in an event
// array, which the text path the MCP writers take does not reach, so deja
// still cannot edit it — but it says so, and leaves a file it has nothing to
// change in alone instead of calling it broken (#5163).
func cursorHooksUpdate(exe string, uninstall, write bool) (installResult, error) {
	path := filepath.Join(sources.CursorCLIHome(), "hooks.json")
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var root map[string]any
	source := old
	commented := false
	var probe any
	if json.Unmarshal(old, &probe) != nil {
		// Comments only, the way cursor strips them; a trailing comma is as
		// broken to cursor's JSON.parse as it is here.
		if stripped := []byte(stripJSONComments(string(old))); json.Unmarshal(stripped, &probe) == nil {
			source, commented = stripped, true
		}
	}
	if len(bytes.TrimSpace(source)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(source, &root); err != nil {
		return installResult{}, configParseError(path, err)
	} else if root == nil {
		return installResult{}, configParseError(path, errors.New("the file holds null, not an object"))
	}
	before, _ := json.Marshal(root)
	if !uninstall {
		root["version"] = 1
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	hooksBefore, _ := json.Marshal(hooks)
	// sessionStart carries the digest. beforeSubmitPrompt only fires in the
	// interactive TUI — headless `-p` skips it — but costs nothing there.
	setCursorHook(hooks, "sessionStart", hookRun(exe, "hook-context"), "", uninstall)
	setCursorHook(hooks, "beforeSubmitPrompt", hookRun(exe, "hook-prompt"), "", uninstall)
	// And the same three cursor already runs for anyone who also has Claude
	// Code: it reads ~/.claude/settings.json and maps the names onto its own —
	// PreToolUse→preToolUse, PostToolUse→postToolUse, PreCompact→preCompact
	// (the table is in its own bundle, 2026.07.23). A cursor-only user got none
	// of them. The dedupe is by exact command string, so a user with both still
	// gets one hook, not two.
	//
	// The matchers keep deja from starting on every read and grep: cursor
	// tests an entry's matcher as a regex against tool_name (Shell, Write,
	// Read, Grep, Task, …) for the three tool events, 2026.09.02.
	setCursorHook(hooks, "preToolUse", hookRun(exe, "hook-tool"), cursorPreToolMatcher, uninstall)
	setCursorHook(hooks, "postToolUse", hookRun(exe, "hook-tool-after"), cursorShellMatcher, uninstall)
	// A tool that errors rather than returns fires this instead, with the
	// failure under error_message, and its additional_context reaches the
	// model the same way.
	setCursorHook(hooks, "postToolUseFailure", hookRun(exe, "hook-tool-after"), cursorShellMatcher, uninstall)
	setCursorHook(hooks, "preCompact", hookRun(exe, "hook-precompact"), "", uninstall)
	// cursor-agent 2026.09.02 runs sessionEnd with conversation_id, which is
	// what clears the live stamp so the next session's MCP recall can answer
	// with this one (#4545).
	setCursorHook(hooks, "sessionEnd", hookRun(exe, "hook-session-end"), "", uninstall)
	// Nothing of deja's came out: the reader's `version` and empty block are
	// theirs, and a commented file is not refused for them.
	if hooksAfter, _ := json.Marshal(hooks); uninstall && string(hooksAfter) == string(hooksBefore) {
		return installResult{Path: path, Action: "unchanged"}, nil
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
		delete(root, "version")
	}
	if len(root) == 0 && len(bytes.TrimSpace(old)) == 0 {
		return installResult{Path: path, Action: "unchanged"}, nil
	}
	after, _ := json.Marshal(root)
	changed := string(after) != string(before)
	if commented {
		if !changed {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		return installResult{}, fmt.Errorf("%s: deja cannot edit hooks in a file that carries comments — add or remove the hook by hand, or take the comments out", path)
	}
	if !write {
		if !changed {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		return installResult{Path: path}, configWritable(path)
	}
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

// Cursor's tool names for a command, a file write (Edit maps onto Write) and
// a subagent spawn.
const (
	cursorPreToolMatcher = "^(Shell|Write|Task)$"
	cursorShellMatcher   = "^Shell$"
)

func setCursorHook(hooks map[string]any, event, cmd, matcher string, uninstall bool) {
	entries, isList := hooks[event].([]any)
	// On the way out a value deja did not write is left as it is: an empty
	// list, or a shape cursor would not read, is not deja's to tidy.
	if uninstall && (!isList || len(entries) == 0) {
		return
	}
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
			setHookMatcher(entry, matcher)
		}
		kept = append(kept, entryAny)
	}
	if !uninstall && !found {
		entry := map[string]any{"command": cmd}
		setHookMatcher(entry, matcher)
		kept = append(kept, entry)
	}
	if len(kept) == 0 {
		delete(hooks, event)
		return
	}
	hooks[event] = kept
}
