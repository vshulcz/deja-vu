package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// vsCodeDefaultUserDir is the stable "Code" host's User folder on this OS —
// where deja writes mcp.json when no VS Code is installed yet, so the config is
// ready the way cursor's and gemini's are before their harness runs. Same
// layout CopilotChatRoots resolves, without the exists check.
func vsCodeDefaultUserDir() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(sources.Home(), "Library", "Application Support", "Code", "User")
	case "windows":
		app := os.Getenv("APPDATA")
		if app == "" {
			app = filepath.Join(sources.Home(), "AppData", "Roaming")
		}
		return filepath.Join(app, "Code", "User")
	default:
		cfg := os.Getenv("XDG_CONFIG_HOME")
		if cfg == "" {
			cfg = filepath.Join(sources.Home(), ".config")
		}
		return filepath.Join(cfg, "Code", "User")
	}
}

// VS Code Copilot Chat has no hook and no plugin channel an outside CLI can
// reach: recall arrives as MCP tools instead. Agent mode reads them from
// mcp.json in the profile's User folder, and the config shape is VS Code's own
// — a top-level `servers` map (not `mcpServers`) whose entries carry a `type`.
// Verified against VS Code 1.134.0 (serversKey "servers", type "stdio").
//
// One file per host that exists — Code, Insiders, VSCodium — because a machine
// can run more than one and each keeps its own User folder. The default profile
// reads <User>/mcp.json; a custom profile has its own, which a person who uses
// one wires from MCP: Open User Configuration.
func vsCodeUserDirs() []string {
	if list := os.Getenv("DEJA_VSCODE_USER_DIRS"); list != "" {
		var out []string
		for _, p := range filepath.SplitList(list) {
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	// CopilotChatRoots resolves exactly these User folders, per host and OS, and
	// returns only the ones present — which is what install should wire.
	return sources.CopilotChatRoots()
}

// vsCodeGuidanceDir is the User folder guidance is written into: the first host
// present, or the stable one's default when VS Code has never run here.
func vsCodeGuidanceDir() string {
	if dirs := vsCodeUserDirs(); len(dirs) > 0 {
		return dirs[0]
	}
	return vsCodeDefaultUserDir()
}

// vsCodeFirstRoot is the User folder of a host that is actually here, for the
// detection `--auto` does. It differs from vsCodeGuidanceDir on purpose: that
// one falls back to a default so an install can write ahead of the editor,
// which as a detection would report VS Code on every machine (#3192).
func vsCodeFirstRoot() string {
	if dirs := vsCodeUserDirs(); len(dirs) > 0 {
		return dirs[0]
	}
	return filepath.Join(os.DevNull, "vscode")
}

func installVSCodeMCP(exe string, uninstall bool) (installResult, error) {
	dirs := vsCodeUserDirs()
	if len(dirs) == 0 {
		// No VS Code User folder yet. On install, write the config the stable
		// host will read once it exists — the same "ready before the harness"
		// the cursor and gemini targets give. On uninstall there is nothing to
		// remove.
		if uninstall {
			return installResult{Path: filepath.Join(vsCodeDefaultUserDir(), "mcp.json"), Action: "unchanged"}, nil
		}
		dirs = []string{vsCodeDefaultUserDir()}
	}
	var last installResult
	for _, dir := range dirs {
		r, err := installVSCodeMCPAt(filepath.Join(dir, "mcp.json"), exe, uninstall)
		if err != nil {
			return installResult{}, err
		}
		last = r
	}
	return last, nil
}

// installVSCodeMCPAt writes one mcp.json. deja's entry is merged into the
// `servers` map and every other server is left untouched; uninstall removes
// only deja's.
func installVSCodeMCPAt(path, exe string, uninstall bool) (installResult, error) {
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	// VS Code writes mcp.json with comments and trailing commas by default, so a
	// strict parse would refuse a file the editor itself produced. Route JSONC
	// through the comment-preserving path, the way the other MCP targets do.
	if len(bytes.TrimSpace(old)) > 0 && configIsJSONC(old) {
		command, args := mcpCommandArgs(exe)
		return writeJSONCEntry(path, old, "servers",
			map[string]any{"type": "stdio", "command": command, "args": args}, uninstall)
	}
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return installResult{}, configParseError(path, err)
	}
	servers, _ := root["servers"].(map[string]any)
	if servers == nil {
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		servers = map[string]any{}
		root["servers"] = servers
	}
	if uninstall {
		delete(servers, "deja")
		if len(servers) == 0 {
			delete(root, "servers")
		}
	} else {
		command, args := mcpCommandArgs(exe)
		servers["deja"] = map[string]any{"type": "stdio", "command": command, "args": args}
	}
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
