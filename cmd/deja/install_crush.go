package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
)

// Crush keeps one global config, and both surfaces deja reaches live in it:
// `mcp` is where the tool comes from and `hooks.PreToolUse` is where recall
// arrives at the point of action. Its MCP block is keyed `mcp` rather than the
// common `mcpServers`, and its hook entries are flat — {name, matcher, command,
// timeout} — with no nested handler list.
//
// PreToolUse is the only event Crush fires (internal/hooks/hooks.go names one
// constant), so there is no session-start or per-prompt channel here: the
// command-and-file line is the whole of auto-recall. A hook answers with
// {"context": …} rather than Claude's nested envelope, which is what
// `deja hook-tool --crush` writes.
//
// Measured on crush v0.92.0 against a recording endpoint (#2949).
func crushConfigDir() string {
	if p := os.Getenv("CRUSH_GLOBAL_CONFIG"); p != "" {
		return p
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "crush")
	}
	return filepath.Join(homeDir(), ".config", "crush")
}

func crushConfigPath() string { return filepath.Join(crushConfigDir(), "crush.json") }

// crushHookMatcher is a regexp against the tool name, so it has to name Crush's
// own tools. The four that act: the shell, and the three that change a file.
// Left wide, the hook would fire on every view, ls, grep and glob as well, and
// pay the index read on each of them for nothing.
const crushHookMatcher = "^(bash|edit|write|multiedit)$"

func installCrushMCP(exe string, uninstall bool) (installResult, error) {
	path := crushConfigPath()
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	root, err := crushRoot(path, old)
	if err != nil {
		return installResult{}, err
	}
	var note string
	m, _, err := mcpBlock(root, "mcp", path)
	if err != nil {
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		return installResult{}, err
	}
	if m == nil {
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		m = map[string]any{}
		root["mcp"] = m
		noteBlockAdded(path, "mcp")
	}
	if uninstall {
		delete(m, "deja")
		removeAdoptedDejaEntries(path, "mcp", m)
		note = leftDejaEntriesNote(m)
		if len(m) == 0 && blockWasAdded(path, "mcp") {
			delete(root, "mcp")
			forgetBlockAdded(path, "mcp")
		}
	} else {
		key := dejaEntryKey(m)
		if key != "deja" {
			noteBlockAdded(path, "mcp."+key)
		}
		m[key], note = mergeDejaEntry(m[key], mcpServerEntry(exe))
		note = withOtherDejaEntries(note, m, key)
	}
	return writeCrushRoot(path, old, root, note)
}

// installCrushAuto wires the PreToolUse hook. It shares crush.json with the MCP
// entry, so `crush-auto` writes this half first: a refusal after the entry was
// written leaves the target reported as refused with half its wiring in the
// file (#2745, the shape qwen-auto is ordered against).
func installCrushAuto(exe string, uninstall bool) (installResult, error) {
	exe = hookExeFor(exe, uninstall)
	path := crushConfigPath()
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	root, err := crushRoot(path, old)
	if err != nil {
		return installResult{}, err
	}
	hooks, _, err := mcpBlock(root, "hooks", path)
	if err != nil {
		// Someone else's shape under the key deja would write into. On the way
		// out there is nothing of deja's in a block it never wrote, and
		// refusing there would leave the rest of the target wired (#2399).
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		return installResult{}, err
	}
	if hooks == nil {
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		hooks = map[string]any{}
		root["hooks"] = hooks
		noteBlockAdded(path, "hooks")
	}
	cmd := shellQuoteIfNeeded(exe) + " hook-tool --crush"
	list, _ := hooks["PreToolUse"].([]any)
	kept := make([]any, 0, len(list))
	for _, item := range list {
		if e, isMap := item.(map[string]any); isMap {
			// Asked about the subcommand rather than the whole line: the
			// matcher reads the last word as the subcommand, and this one ends
			// in a flag.
			if s, _ := e["command"].(string); isDejaHookCommand(s, "deja hook-tool") {
				continue
			}
		}
		kept = append(kept, item)
	}
	if !uninstall {
		kept = append(kept, map[string]any{
			"name":    "deja",
			"matcher": crushHookMatcher,
			"command": cmd,
			// Ten seconds, not the default thirty: this runs inside an action
			// the user is waiting on, and a hook that cannot answer fast has
			// nothing worth waiting for.
			"timeout": 10,
		})
	}
	if len(kept) == 0 {
		delete(hooks, "PreToolUse")
		if len(hooks) == 0 && blockWasAdded(path, "hooks") {
			delete(root, "hooks")
			forgetBlockAdded(path, "hooks")
		}
	} else {
		hooks["PreToolUse"] = kept
	}
	return writeCrushRoot(path, old, root, hookStatusMessage("PreToolUse"))
}

func crushRoot(path string, old []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(old)) == 0 {
		return map[string]any{}, nil
	}
	var root map[string]any
	if err := json.Unmarshal(old, &root); err != nil {
		return nil, configParseError(path, err)
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, nil
}

func writeCrushRoot(path string, old []byte, root map[string]any, note string) (installResult, error) {
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return installResult{}, err
	}
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a, Note: note}, err
}

// crushCommandPath is where Crush reads custom commands from: the global config
// directory's commands folder, which it lists under the `user:` prefix.
func crushCommandPath() string {
	return filepath.Join(crushConfigDir(), "commands", "deja.md")
}
