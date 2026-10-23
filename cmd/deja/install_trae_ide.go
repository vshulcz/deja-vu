package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// TRAE IDE is a VS Code fork with its agent in a native library, read off
// 3.5.104 (macOS). Its chats are in an encrypted database, so deja wires it
// and does not read it.
//
// MCP: <user data>/User/mcp.json, {"mcpServers":{"deja":{"command","args"}}}.
// The schema has additionalProperties false, so no type key. Writing the file
// while the IDE runs loads the server; the agent called deja from chat.
//
// Hooks: ~/<dataFolderName>/hooks.json, Claude's shape and event names. The
// IDE ships with hooks off: they run only once Settings > Hooks has global
// hooks on, and that switch is kept in the agent's own configuration store,
// not in a file deja can read. Live payloads carry session_id, cwd and
// workspace_roots, and tool events name the tool twice, as tool_name and
// llm_tool_name. ~/.trae/hooks.json is also
// where TRAE CLI 1.x kept hooks; TRAE CLI 2.0 no longer reads it.
//
// Skill: <userHome>/<dataFolderName>/skills/<name>/SKILL.md, the global root
// the IDE's own skill installer names. TRAE CLI 0.208 lists that root too and
// shows a single deja-history when ~/.agents/skills holds the same skill.
//
// The CN build is the same app named "Trae CN", with ~/.trae-cn. Either or
// both can be installed, and each build present is wired in its own files.

type traeIDEEdition struct{ app, dataFolder string }

var traeIDEEditions = []traeIDEEdition{{"Trae", ".trae"}, {"Trae CN", ".trae-cn"}}

// traeIDEAppDir is where an edition keeps its user data, in VS Code's layout.
func traeIDEAppDir(app string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(sources.Home(), "Library", "Application Support", app)
	case "windows":
		dir := os.Getenv("APPDATA")
		if dir == "" {
			dir = filepath.Join(sources.Home(), "AppData", "Roaming")
		}
		return filepath.Join(dir, app)
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(sources.Home(), ".config")
	}
	return filepath.Join(cfg, app)
}

func (e traeIDEEdition) userDir() string { return filepath.Join(traeIDEAppDir(e.app), "User") }

func (e traeIDEEdition) mcpPath() string { return filepath.Join(e.userDir(), "mcp.json") }

func (e traeIDEEdition) hooksPath() string {
	return filepath.Join(sources.Home(), e.dataFolder, "hooks.json")
}

func (e traeIDEEdition) skillPath() string {
	return filepath.Join(sources.Home(), e.dataFolder, "skills", "deja-history", "SKILL.md")
}

// traeIDEPresent is every edition with user data on this machine, or the
// international one when neither has any yet. Both builds can be installed
// side by side, and each reads only its own files.
func traeIDEPresent() []traeIDEEdition {
	var out []traeIDEEdition
	for _, e := range traeIDEEditions {
		if _, err := os.Stat(e.userDir()); err == nil {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		out = traeIDEEditions[:1]
	}
	return out
}

// traeIDEUninstalling is every edition whose files deja may have written:
// all of them, so a build removed since the install still gets its files
// back, while a file that is not there is left not there.
func traeIDEUninstalling() []traeIDEEdition { return traeIDEEditions }

// The first present edition's paths, for the single-path callers: guidance
// status, detection and the doctor golden.
func traeIDEUserDir() string   { return traeIDEPresent()[0].userDir() }
func traeIDEMCPPath() string   { return traeIDEPresent()[0].mcpPath() }
func traeIDEHooksPath() string { return traeIDEPresent()[0].hooksPath() }
func traeIDESkillPath() string { return traeIDEPresent()[0].skillPath() }

// traeIDEHookWiring has no SessionEnd: the IDE fires no such event, and its
// Stop ends a turn, not the session. A live run sent Claude's tool names,
// tool_name "Write" with file_path in tool_input, so the matchers are Claude's.
var traeIDEHookWiring = []hookWire{
	{"SessionStart", "hook-context", ""},
	{"UserPromptSubmit", "hook-prompt", ""},
	{"PreToolUse", "hook-tool", "Edit|Write|MultiEdit"},
	{"PostToolUse", "hook-tool-after", "Bash"},
	{"PreCompact", "hook-precompact", ""},
}

// traeIDEHooksOffNote is said under the hooks wherever deja reports them: the
// switch cannot be read, and the IDE's default is off.
const traeIDEHooksOffNote = "TRAE IDE runs these only once hooks are on: Settings > Hooks, enable global hooks and run them locally"

func traeIDEEditionsFor(uninstall bool) []traeIDEEdition {
	if uninstall {
		return traeIDEUninstalling()
	}
	return traeIDEPresent()
}

// installTraeIDE writes the MCP entry for every edition. The first edition's
// skill is the guidance step's; the others get the same file here.
func installTraeIDE(exe string, uninstall bool) (installResult, error) {
	editions := traeIDEEditionsFor(uninstall)
	var rs []installResult
	for i, e := range editions {
		if !uninstall || fileExists(e.mcpPath()) {
			r, err := installMCPJSON(e.mcpPath(), exe, uninstall)
			if err != nil {
				return installResult{}, err
			}
			rs = append(rs, r)
		}
		if (i == 0 && !uninstall) || e.skillPath() == guidancePath("trae-ide") {
			continue
		}
		sk, err := traeIDESkill(e.skillPath(), uninstall)
		if err != nil {
			return installResult{}, err
		}
		if sk.Path != "" {
			rs = append(rs, sk)
		}
	}
	if len(rs) == 0 {
		return installResult{Path: traeIDEMCPPath(), Action: "unchanged"}, nil
	}
	return wroteAll(rs...), nil
}

// traeIDESkill writes or removes the skill for an edition past the first,
// taking out only a copy that is deja's.
func traeIDESkill(path string, uninstall bool) (installResult, error) {
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return installResult{}, err
	}
	if uninstall {
		if len(old) == 0 || !isOurGuidance(old, "trae-ide") {
			return installResult{}, nil
		}
		if rerr := os.Remove(path); rerr != nil {
			return installResult{}, rerr
		}
		return installResult{Path: path, Action: "removed"}, nil
	}
	noteCreatedDirs(filepath.Dir(path))
	if merr := os.MkdirAll(filepath.Dir(path), 0o755); merr != nil {
		return installResult{}, merr
	}
	a, err := writeGuidanceFile(path, old, []byte(guidanceText("trae-ide")))
	return installResult{Path: path, Action: a}, err
}

// installTraeIDEAuto writes the hooks first: a hooks.json deja refuses should
// leave nothing half-wired (#2745). Every edition's file is checked before any
// is written. The IDE pins no trust, so no config path.
func installTraeIDEAuto(exe string, uninstall bool) (installResult, error) {
	editions := traeIDEEditionsFor(uninstall)
	for _, e := range editions {
		if err := readableStrictJSON(e.hooksPath()); err != nil {
			return installResult{}, err
		}
	}
	var rs []installResult
	for _, e := range editions {
		if uninstall && !fileExists(e.hooksPath()) {
			continue
		}
		r, err := installCodexHooksAt(exe, e.hooksPath(), "", traeIDEHookWiring, uninstall)
		if err != nil {
			return installResult{}, err
		}
		rs = append(rs, r)
	}
	mcp, err := installTraeIDE(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	if !uninstall {
		fmt.Println("trae-ide: hooks are off by default in TRAE IDE — turn them on in Settings > Hooks (global hooks, run locally); until then only the MCP tool works")
	}
	return wroteAll(append(rs, mcp)...), nil
}
