package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
)

// Muse Code keeps MCP servers and hooks in one user file,
// ${XDG_CONFIG_HOME:-~/.config}/muse/settings.json. Measured on 1.4.2
// (1.4.2-R4684.1) against a local provider stub, no login (#4709):
//
// MCP: `mcpServers` entries load as an `mcp__<name>` tool namespace, and a
// call to mcp__deja__deja answered from inside the session. The legacy key
// `mcp_servers` still loads on its own, but with both keys present Muse drops
// every server, so deja writes into whichever one the file already has. The
// entry carries `type` and `mode: optional` because Muse's own migrate skill
// writes added servers that way: an optional server that fails to start never
// holds up Muse's startup.
//
// Hooks: the Claude Code shape, {"hooks":{Event:[{matcher, hooks:[{type,
// command, timeout}]}]}}, which Muse parses as "foreign" hooks with timeout in
// seconds. They fire in an untrusted workspace too. The payload is Claude's
// (hook_event_name, tool_name, tool_input, session_id, cwd; transcript_path is
// null), and SessionStart's additionalContext, a UserPromptSubmit line and a
// PreToolUse additionalContext all reach the next request as a developer
// message. Muse's tools are bash, edit_file and write_file, the last two with
// the file under `path`; workflow is how it starts a subagent.

func museSettingsPath() string {
	return filepath.Join(xdgConfigHome(), "muse", "settings.json")
}

// museSettingsSeed is the smallest settings file Muse starts with. Without
// schema_version 1.4.1 and 1.4.2 refuse to start ("malformed settings file …
// missing field `schema_version`"), and Muse writes no settings file of its
// own, so a first install is what creates it (#4736).
const museSettingsSeed = "{\n  \"schema_version\": 1\n}\n"

// seedMuseSettings writes museSettingsSeed into a settings file that is missing
// or empty, so what deja adds next lands in a file Muse accepts. An uninstall
// that leaves only the seed behind removes the file it created, or empties the
// one the reader had (structurallyEmptyConfig).
func seedMuseSettings(path string) (string, error) {
	old, err := readConfig(path)
	if err != nil || len(bytes.TrimSpace(old)) != 0 {
		return "unchanged", err
	}
	return writeIfChanged(path, old, []byte(museSettingsSeed))
}

// seededResult reports a file the seed created as created, which is what the
// reader sees happen; the writer after it found a file and says updated.
func seededResult(seed string, r installResult) installResult {
	if seed == "created" && r.Action != "unchanged" {
		r.Action = "created"
	}
	return r
}

// museHookWiring is every event deja installs into Muse.
var museHookWiring = []struct{ Event, Sub, Matcher string }{
	{"SessionStart", "hook-context", ""},
	{"UserPromptSubmit", "hook-prompt", ""},
	{"PreToolUse", "hook-tool", "bash|edit_file|write_file"},
	{"PostToolUse", "hook-tool-after", "bash"},
	{"PostToolUseFailure", "hook-tool-after", "bash"},
	// Muse compacts in the middle of a turn, before a request that would not
	// fit, and fires PreCompact first; the packet goes out on the next
	// PreToolUse or prompt, since PostCompact takes no context and SessionStart
	// does not fire again (#4737).
	{"PreCompact", "hook-precompact", ""},
	{"SessionEnd", "hook-session-end", ""},
}

// museHookTimeout is in seconds, as Claude Code's is.
const museHookTimeout = 60

// museMCPKey is the block deja writes into: the legacy one when the file has
// only that, since adding the other beside it switches every server off.
func museMCPKey(path string) (string, error) {
	old, err := readConfig(path)
	if err != nil || len(bytes.TrimSpace(old)) == 0 {
		return "mcpServers", err
	}
	var root map[string]any
	if json.Unmarshal([]byte(jsoncToJSON(string(old))), &root) != nil {
		// Not ours to diagnose here: the writer reports the parse error.
		return "mcpServers", nil //nolint:nilerr // the writer reports the parse error
	}
	// A key set to null holds no servers, so it counts as absent, as it
	// does for doctor.
	legacy := root["mcp_servers"] != nil
	current := root["mcpServers"] != nil
	switch {
	case legacy && current:
		return "", fmt.Errorf("%s has both mcp_servers and mcpServers, and Muse loads no MCP server at all while both are there — move the entries under mcpServers and run this again", path)
	case legacy:
		return "mcp_servers", nil
	}
	return "mcpServers", nil
}

func installMuseMCP(exe string, uninstall bool) (installResult, error) {
	path := museSettingsPath()
	command, args := mcpCommandArgs(exe)
	entry := map[string]any{"type": "stdio", "command": command, "args": args, "mode": "optional"}
	if uninstall {
		// Out of both blocks: the user may have added the other one since
		// the install, and deja's entry is still in the file either way.
		var res installResult
		for i, key := range []string{"mcpServers", "mcp_servers"} {
			r, err := installMCPJSONEntry(path, key, entry, true)
			if err != nil {
				return installResult{}, err
			}
			if i == 0 || (res.Action == "unchanged" && r.Action != "unchanged") {
				res = r
			}
		}
		return res, nil
	}
	key, err := museMCPKey(path)
	if err != nil {
		return installResult{}, err
	}
	seed, err := seedMuseSettings(path)
	if err != nil {
		return installResult{}, err
	}
	r, err := installMCPJSONEntry(path, key, entry, false)
	return seededResult(seed, r), err
}

func installMuseHooks(exe string, uninstall bool) (installResult, error) {
	exe = hookExeFor(exe, uninstall)
	path := museSettingsPath()
	seed := "unchanged"
	if !uninstall {
		var err error
		if seed, err = seedMuseSettings(path); err != nil {
			return installResult{}, err
		}
	}
	var res installResult
	for i, h := range museHookWiring {
		r, err := installSettingsHookCmd(path, h.Event, h.Matcher, museHookTimeout, hookRun(exe, h.Sub), uninstall)
		if err != nil {
			return installResult{}, err
		}
		if i == 0 || (res.Action == "unchanged" && r.Action != "unchanged") {
			res = r
		}
	}
	return seededResult(seed, res), nil
}

// installMuseAuto writes the hooks first: a settings file deja refuses
// should leave nothing half-wired (#2745).
func installMuseAuto(exe string, uninstall bool) (installResult, error) {
	// The MCP block's refusal comes before any hook is written, for the same
	// reason.
	if !uninstall {
		if _, err := museMCPKey(museSettingsPath()); err != nil {
			return installResult{}, err
		}
	}
	hooks, err := installMuseHooks(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	mcp, err := installMuseMCP(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(hooks, mcp), nil
}
