package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// ZCode's runtime keeps everything in one file — `~/.zcode/cli/setting.json` —
// and both halves of an install live in it: the server under `mcp.servers`,
// and the hooks under `hooks.events`, each event in Claude Code's shape
// (`hooks.events.<Event>[].hooks[] = {type, command, timeout}`).
//
// deja used to write `~/.zcode/cli/config.json`, with the events directly
// under `hooks`: the shape volcengine/OpenViking's memory plugin recorded from
// an older ZCode (examples/agent-hook-plugin/DESIGN.md). The 3.14.4 runtime
// reads config.json once, as the source of a first-launch migration, so on any
// machine where ZCode had run, nothing deja wrote reached the agent (#4429).
// What still holds from that design: config-file hooks need
// `hooks.enabled: true`, a config hook gets no template expansion — so the
// command carries an absolute path — and the output schema discards the whole
// response over one unrecognised key, which is what `--strict` is for.
//
// Two events are wired, the two deja has something to say at: SessionStart
// puts the project's memory in front of the model before the first prompt, and
// UserPromptSubmit answers the prompt that was just typed (#3651).
func zcodeConfigPath() string {
	return filepath.Join(sources.ZCodeConfigDir(), "cli", "setting.json")
}

// zcodeLegacyConfigPath is where deja wrote before #4429. An uninstall clears
// it too, or ZCode's migration could bring a removed server back.
func zcodeLegacyConfigPath() string {
	return filepath.Join(sources.ZCodeConfigDir(), "cli", "config.json")
}

// readZCodeSetting reads the file an install edits. Before ZCode's first
// launch setting.json is missing, and the runtime builds it from config.json,
// minus the provider fields, only while it is: a file deja created first took
// that migration away, and the reader's servers, permissions and hooks never
// reached the runtime. So an install starts it the way the runtime would,
// unless the runtime's marker says it has already migrated (#4429).
func readZCodeSetting(path string, uninstall bool) ([]byte, map[string]any, error) {
	old, err := readConfig(path)
	if err != nil {
		return nil, nil, err
	}
	seeded := false
	if len(old) == 0 && !uninstall && path == zcodeConfigPath() && !fileExists(path) &&
		!fileExists(filepath.Join(filepath.Dir(path), "migrations", "settings-v1.json")) {
		if b, err := readConfig(zcodeLegacyConfigPath()); err == nil && len(b) > 0 {
			old, seeded = b, true
		}
	}
	root := map[string]any{}
	if len(old) > 0 {
		if err := json.Unmarshal(old, &root); err != nil {
			if seeded {
				// The runtime refuses it too; there is nothing to carry over.
				return nil, map[string]any{}, nil
			}
			return nil, nil, configParseError(path, err)
		}
	}
	if seeded {
		if root == nil {
			root = map[string]any{}
		}
		delete(root, "provider")
		delete(root, "model")
		delete(root, "modelCatalog")
	}
	return old, root, nil
}

// zcodeAlsoLegacy runs an uninstall on the old file beside the one on the
// current file, and reports the old one only when it changed.
func zcodeAlsoLegacy(res installResult, uninstall bool, edit func(string, bool) (installResult, error)) (installResult, error) {
	if !uninstall {
		return res, nil
	}
	old, err := edit(zcodeLegacyConfigPath(), true)
	if err != nil {
		return installResult{}, err
	}
	if old.Action == "unchanged" {
		return res, nil
	}
	return wroteAll(res, old), nil
}

// installZCode writes the server under `mcp.servers`, which is one level
// deeper than the `mcpServers` every other client here uses, so the shared
// installMCPJSON cannot be pointed at it.
func installZCode(exe string, uninstall bool) (installResult, error) {
	edit := func(path string, uninstall bool) (installResult, error) { return zcodeServerAt(path, exe, uninstall) }
	res, err := edit(zcodeConfigPath(), uninstall)
	if err != nil {
		return installResult{}, err
	}
	return zcodeAlsoLegacy(res, uninstall, edit)
}

func zcodeServerAt(path, exe string, uninstall bool) (installResult, error) {
	old, root, err := readZCodeSetting(path, uninstall)
	if err != nil {
		return installResult{}, err
	}
	var note string
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
		entry := mcpServerEntry(exe)
		note = keepSwitch(servers["deja"], entry)
		servers["deja"] = entry
		mcp["servers"] = servers
		root["mcp"] = mcp
	}
	// In the reader's key order and indent: the runtime writes its own file
	// and a sorted copy of it reads as a rewrite (#4431).
	next, err := marshalConfigLike(old, root)
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
	return installResult{Path: path, Action: action, Note: note}, nil
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
	out := wroteAll(server, hooksRes)
	// Both halves write the same file, so wroteAll keeps one result and the
	// other's note — the hooks switch, or the server's own off switch (#4467)
	// — would go with it.
	for _, n := range []string{server.Note, hooksRes.Note} {
		if n != "" && !strings.Contains(out.Note, n) {
			if out.Note != "" {
				out.Note += "; "
			}
			out.Note += n
		}
	}
	return out, nil
}

func installZCodeHooks(exe string, uninstall bool) (installResult, error) {
	// The launcher, not this binary: a generated plugin is as much a
	// config as a hooks.json, and one that names the build it was
	// installed from stops working the day that build moves (#3682).
	exe = hookExeFor(exe, uninstall)
	edit := func(path string, uninstall bool) (installResult, error) { return zcodeHooksAt(path, exe, uninstall) }
	res, err := edit(zcodeConfigPath(), uninstall)
	if err != nil {
		return installResult{}, err
	}
	return zcodeAlsoLegacy(res, uninstall, edit)
}

func zcodeHooksAt(path, exe string, uninstall bool) (installResult, error) {
	old, root, err := readZCodeSetting(path, uninstall)
	if err != nil {
		return installResult{}, err
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
	if events == nil && !uninstall {
		events = map[string]any{}
		noteBlockAdded(path, "hooks.events")
	}

	wanted := map[string]map[string]any{
		"SessionStart":     zcodeHookEntry(hookRun(exe, "hook-context", "--strict"), 30),
		"UserPromptSubmit": zcodeHookEntry(hookRun(exe, "hook-prompt", "--strict"), 20),
	}
	changed := false
	note := ""
	for event, entry := range wanted {
		// The old shape, events directly under `hooks`, is only ever taken
		// out: the runtime does not look there.
		if list, ok := hooks[event].([]any); ok {
			kept := withoutZCodeHooks(list)
			if len(kept) != len(list) {
				changed = true
				if len(kept) == 0 {
					delete(hooks, event)
				} else {
					hooks[event] = kept
				}
			}
		}
		if events == nil {
			continue
		}
		list, present := events[event].([]any)
		kept := withoutZCodeHooks(list)
		if len(kept) != len(list) {
			changed = true
		}
		if uninstall {
			switch {
			case len(kept) == len(list):
			case len(kept) == 0 && blockWasAdded(path, "hooks.events."+event):
				delete(events, event)
				forgetBlockAdded(path, "hooks.events."+event)
			default:
				// ZCode lists every event, empty, in the file it writes: an
				// empty list it had stays.
				events[event] = kept
			}
			continue
		}
		if !present {
			noteBlockAdded(path, "hooks.events."+event)
		}
		events[event] = append(kept, entry)
		changed = true
	}
	if uninstall {
		if restoreZCodeHooksSwitch(path, hooks) {
			changed = true
		}
		if !changed {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		if events != nil && len(events) == 0 && blockWasAdded(path, "hooks.events") {
			delete(hooks, "events")
			forgetBlockAdded(path, "hooks.events")
			events = nil
		}
	} else if on, _ := hooks["enabled"].(bool); !on {
		// Config-file hooks do not run at all without this, and the plugin
		// that got here first records the same: the merge has to set it. A
		// block the reader had keeps what the switch was, so the uninstall
		// can put it back: the runtime's own file starts with it off, and
		// hooks the reader switched off would otherwise run from then on
		// (#4431, the rule gemini's hooksConfig.enabled follows — #4216).
		if !blockWasAdded(path, "hooks") && len(blocksAddedWithPrefix(path, zcodeSwitchRecord)) == 0 {
			was := ""
			if v, ok := hooks["enabled"]; ok {
				b, _ := json.Marshal(v)
				was = string(b)
			}
			noteBlockAdded(path, zcodeSwitchRecord+was)
			note = "turned hooks.enabled on in " + shortHome(path) + ", which was off, so its other hooks run too; uninstall turns it back off"
		}
		hooks["enabled"] = true
		changed = true
	}
	if events != nil {
		hooks["events"] = events
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
	// In the reader's key order and indent: the runtime writes its own file
	// and a sorted copy of it reads as a rewrite (#4431).
	next, err := marshalConfigLike(old, root)
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
	return installResult{Path: path, Action: action, Note: note}, nil
}

// zcodeSwitchRecord names the record of what hooks.enabled was before deja
// turned it on: the JSON value after the =, or nothing when the key was absent.
const zcodeSwitchRecord = "hooks.enabled="

// restoreZCodeHooksSwitch puts hooks.enabled back the way the reader had it,
// when deja is the one that turned it on, and reports whether it did.
func restoreZCodeHooksSwitch(path string, hooks map[string]any) bool {
	names := blocksAddedWithPrefix(path, zcodeSwitchRecord)
	for _, name := range names {
		forgetBlockAdded(path, name)
		was := strings.TrimPrefix(name, zcodeSwitchRecord)
		if was == "" {
			delete(hooks, "enabled")
			continue
		}
		var v any
		if json.Unmarshal([]byte(was), &v) == nil {
			hooks["enabled"] = v
		}
	}
	return len(names) > 0
}

// zcodeHooksRun reports whether a setting.json has one of deja's hooks where
// the runtime runs it: under hooks.events, with hooks.enabled on. A file
// ZCode migrated from an older deja's config.json has them directly under
// hooks, which the runtime ignores (#4429).
func zcodeHooksRun(b []byte) bool {
	var root struct {
		Hooks struct {
			Enabled bool             `json:"enabled"`
			Events  map[string][]any `json:"events"`
		} `json:"hooks"`
	}
	if json.Unmarshal(bytes.TrimPrefix(b, utf8BOM), &root) != nil || !root.Hooks.Enabled {
		return false
	}
	for _, list := range root.Hooks.Events {
		if len(withoutZCodeHooks(list)) != len(list) {
			return true
		}
	}
	return false
}

// withoutZCodeHooks is an event's list with deja's entries taken out.
func withoutZCodeHooks(list []any) []any {
	kept := make([]any, 0, len(list))
	for _, item := range list {
		if !zcodeEntryIsOurs(item) {
			kept = append(kept, item)
		}
	}
	return kept
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
