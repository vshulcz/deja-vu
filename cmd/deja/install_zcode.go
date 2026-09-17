package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// ZCode keeps everything in one file — `~/.zcode/cli/config.json` — and both
// halves of an install live in it: the server under `mcp.servers`, and the
// hooks under `hooks`, which is Claude Code's shape down to the nesting
// (`hooks.<Event>[].hooks[] = {type, command, timeout}`).
//
// The shapes are not guessed. They are the surface volcengine/OpenViking's
// memory plugin established by inspecting a live install and shipped an
// installer against (examples/agent-hook-plugin/DESIGN.md and
// hosts/zcode/{hooks.json,.mcp.json}): seven hook events, config-file hooks
// requiring `hooks.enabled: true`, no template expansion in a config hook — so
// the command carries an absolute path — and a strict output schema that
// discards the whole response over one unrecognised key, which is what
// `--strict` is for.
//
// Two events are wired, the two deja has something to say at: SessionStart
// puts the project's memory in front of the model before the first prompt, and
// UserPromptSubmit answers the prompt that was just typed (#3651).
func zcodeConfigPath() string {
	return filepath.Join(sources.ZCodeConfigDir(), "cli", "config.json")
}

// installZCode writes the server under `mcp.servers`, which is one level
// deeper than the `mcpServers` every other client here uses, so the shared
// installMCPJSON cannot be pointed at it.
func installZCode(exe string, uninstall bool) (installResult, error) {
	path := zcodeConfigPath()
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	root := map[string]any{}
	if len(old) > 0 {
		if err := json.Unmarshal(old, &root); err != nil {
			return installResult{}, configParseError(path, err)
		}
	}
	mcp, _ := root["mcp"].(map[string]any)
	if mcp == nil {
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		mcp = map[string]any{}
		// Both containers are recorded, because both may be deja's own: the
		// uninstall took its entry out and left `"mcp": {"servers": {}}` in a
		// file it had created itself, which is what the record exists to
		// prevent (#2604, and this writer is new enough to have missed it —
		// #3690).
		noteBlockAdded(path, "mcp")
	}
	servers, _ := mcp["servers"].(map[string]any)
	if servers == nil {
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		servers = map[string]any{}
		noteBlockAdded(path, "mcp.servers")
	}
	if uninstall {
		if _, ok := servers["deja"]; !ok {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		delete(servers, "deja")
		mcp["servers"] = servers
		if len(servers) == 0 && blockWasAdded(path, "mcp.servers") {
			delete(mcp, "servers")
			forgetBlockAdded(path, "mcp.servers")
		}
		root["mcp"] = mcp
		if len(mcp) == 0 && blockWasAdded(path, "mcp") {
			delete(root, "mcp")
			forgetBlockAdded(path, "mcp")
		}
	} else {
		servers["deja"] = mcpServerEntry(exe)
		mcp["servers"] = servers
		root["mcp"] = mcp
	}
	next, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	noteCreatedDirs(filepath.Dir(path))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return installResult{}, err
	}
	action, err := writeIfChanged(path, old, next)
	if err != nil {
		return installResult{}, err
	}
	return installResult{Path: path, Action: action}, nil
}

// zcodeHookEntry is one wired event, in the shape the config takes.
func zcodeHookEntry(command string, timeout int) map[string]any {
	return map[string]any{
		"hooks": []any{map[string]any{
			"type":    "command",
			"command": command,
			"timeout": timeout,
		}},
	}
}

func installZCodeAuto(exe string, uninstall bool) (installResult, error) {
	// The server first, then the hooks: `deja install --auto` installs the
	// -auto target alone, and hooks without the server would leave the tool
	// half-wired with nothing saying so.
	server, err := installZCode(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	hooksRes, err := installZCodeHooks(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(server, hooksRes), nil
}

func installZCodeHooks(exe string, uninstall bool) (installResult, error) {
	// The launcher, not this binary: a generated plugin is as much a
	// config as a hooks.json, and one that names the build it was
	// installed from stops working the day that build moves (#3682).
	exe = hookExeFor(exe, uninstall)
	path := zcodeConfigPath()
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	root := map[string]any{}
	if len(old) > 0 {
		if err := json.Unmarshal(old, &root); err != nil {
			return installResult{}, configParseError(path, err)
		}
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		hooks = map[string]any{}
		noteBlockAdded(path, "hooks")
	}
	events, _ := hooks["events"].(map[string]any)
	// The config has held both shapes: OpenViking's installer writes the
	// events at the top of `hooks`, and its own verification step looks for
	// `hooks.events`. Whichever is there is the one we edit, so an install
	// beside theirs does not write a second block that never fires.
	container := hooks
	if events != nil {
		container = events
	}

	wanted := map[string]map[string]any{
		"SessionStart":     zcodeHookEntry(hookRun(exe, "hook-context", "--strict"), 30),
		"UserPromptSubmit": zcodeHookEntry(hookRun(exe, "hook-prompt", "--strict"), 20),
	}
	changed := false
	for event, entry := range wanted {
		list, _ := container[event].([]any)
		kept := make([]any, 0, len(list))
		for _, item := range list {
			if zcodeEntryIsOurs(item) {
				changed = true
				continue
			}
			kept = append(kept, item)
		}
		if uninstall {
			if len(kept) == 0 {
				delete(container, event)
			} else {
				container[event] = kept
			}
			continue
		}
		container[event] = append(kept, entry)
		changed = true
	}
	if uninstall {
		if !changed {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
	} else {
		// Config-file hooks do not run at all without this, and the plugin
		// that got here first records the same: the merge has to set it.
		if on, _ := hooks["enabled"].(bool); !on {
			hooks["enabled"] = true
			changed = true
		}
	}
	if events != nil {
		hooks["events"] = container
	}
	root["hooks"] = hooks
	// A `hooks` block deja added holds nothing but the switch deja turned on
	// once its events are out, and that switch is the reason gemini's stays:
	// something else may be running on it. Here nothing can be — deja created
	// the block — so it goes with them, and a block the reader already had is
	// left exactly as gemini's is (#3690).
	if uninstall && blockWasAdded(path, "hooks") && onlyTheEnabledSwitch(hooks) {
		delete(root, "hooks")
		forgetBlockAdded(path, "hooks")
	}
	next, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	noteCreatedDirs(filepath.Dir(path))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return installResult{}, err
	}
	action, err := writeIfChanged(path, old, next)
	if err != nil {
		return installResult{}, err
	}
	return installResult{Path: path, Action: action}, nil
}

// zcodeEntryIsOurs recognises a hook deja wrote, so a second install replaces
// it instead of stacking another copy, and an uninstall takes only ours.
func zcodeEntryIsOurs(item any) bool {
	m, ok := item.(map[string]any)
	if !ok {
		return false
	}
	list, _ := m["hooks"].([]any)
	for _, h := range list {
		entry, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if cmd, _ := entry["command"].(string); zcodeCommandIsOurs(cmd) {
			return true
		}
	}
	return false
}

// onlyTheEnabledSwitch reports whether a hooks block holds nothing but the
// `enabled` flag — an empty `events` map included, since the events container
// is written back before this is asked.
func onlyTheEnabledSwitch(hooks map[string]any) bool {
	for key, v := range hooks {
		if key == "enabled" {
			continue
		}
		if key == "events" {
			if m, ok := v.(map[string]any); ok && len(m) == 0 {
				continue
			}
		}
		return false
	}
	return true
}

// zcodeCommandIsOurs reports whether a config command line runs one of deja's
// hooks. The shared hookCommandKindOf reads the subcommand off the end of the
// reference command, and the lines deja writes here end in `--strict`, so this
// looks for the pair instead: a deja binary, then a hook subcommand.
func zcodeCommandIsOurs(cmd string) bool {
	fields := strings.Fields(cmd)
	for i := 0; i+1 < len(fields); i++ {
		// hookTokenIsDejas, not isDejaBinaryToken: the pair is what identifies
		// the line, so a build under another name is still deja's — the test
		// binary is `deja.test.exe`, and on Windows that read as a stranger's
		// hook and the uninstall left the whole block (#3681 gave the other
		// writers this predicate; this one kept the narrow test).
		if hookTokenIsDejas(fields[i]) && hookNames[fields[i+1]] {
			return true
		}
	}
	return false
}
