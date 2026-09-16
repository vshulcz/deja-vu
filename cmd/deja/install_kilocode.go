package main

import (
	"os"
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Kilo Code takes two of the channels deja has, and neither is Roo's.
//
// The MCP server goes in `<globalStorage>/kilocode.kilo-code/settings/mcp_settings.json`
// — the same file Cline and Roo carry, named by Kilo's own
// `KilocodePaths.vscodeGlobalStorage()` in packages/opencode/src/kilocode/paths.ts.
// One per host, because the same person runs Kilo in VS Code and in Cursor and
// wiring one leaves the other without recall (#3233 is that failure in Roo).
//
// A skill goes in `~/.kilocode/skills/<name>/SKILL.md`. Kilo reads skills from
// `~/.kilocode/skills` and `~/.kilo/skills` as well as the project tree — the
// glob is in the same paths.ts — so the shared manual reaches it without a
// per-project file.
//
// Hooks it does not have: a search of the repository for a hook surface finds
// none, and the extension is a Roo fork whose hooks are still in flight
// upstream. So auto-recall stays a gap with a reason rather than a promise.
func kilocodeMCPSettingsPaths() []string {
	var out []string
	for _, root := range sources.KiloRoots() {
		out = append(out, filepath.Join(root, "settings", "mcp_settings.json"))
	}
	return out
}

// kilocodeSkillPath is where Kilo looks for a user-level skill. `.kilocode`
// first: Kilo's own loader lists it before `.kilo`, and writing the one it
// reads first is what a reader will find.
func kilocodeSkillPath() string {
	return filepath.Join(sources.Home(), ".kilocode", "skills", "deja-search", "SKILL.md")
}

func installKilocode(exe string, uninstall bool) (installResult, error) {
	paths := kilocodeMCPSettingsPaths()
	var results []installResult
	seen := 0
	for _, p := range paths {
		// Only a host Kilo has actually run in: creating the directory would
		// leave settings behind for an editor that does not have the extension.
		if _, err := os.Stat(filepath.Dir(filepath.Dir(p))); err != nil {
			continue
		}
		seen++
		res, err := installMCPJSON(p, exe, uninstall)
		if err != nil {
			return installResult{}, err
		}
		results = append(results, res)
	}

	// The skill is a user-level file and this target was asked for by name, so
	// it is written whether or not an editor carries the extension — the CLI is
	// the other half of Kilo and reads the same directory. `--auto` does not
	// come through here unless kilocodeFirstRoot found the store.
	skillRes, err := installKilocodeSkill(uninstall)
	if err != nil {
		return installResult{}, err
	}
	if skillRes.Path != "" {
		results = append(results, skillRes)
	}

	out := wroteAll(results...)
	if seen == 0 {
		// Saying nothing here would leave the reader thinking the MCP server is
		// wired in an editor that never had Kilo.
		out.Note = joinNotes(out.Note,
			"no VS Code host has Kilo Code installed, so only the skill was written")
	}
	return out, nil
}

// installKilocodeSkill writes the shared manual where Kilo reads it. The file
// is deja's own, so an edited copy is kept rather than replaced — the rule
// every skill here follows.
func installKilocodeSkill(uninstall bool) (installResult, error) {
	path := kilocodeSkillPath()
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return installResult{}, err
	}
	if uninstall {
		if len(old) == 0 {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		if err := os.Remove(path); err != nil {
			return installResult{}, err
		}
		return installResult{Path: path, Action: "removed"}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return installResult{}, err
	}
	next := []byte(skillFile(skillBody))
	if string(old) == string(next) {
		return installResult{Path: path, Action: "unchanged"}, nil
	}
	if err := writeSkillOfOurs(path, old, next); err != nil {
		return installResult{}, err
	}
	action := "wrote"
	if len(old) > 0 {
		action = "updated"
	}
	return installResult{Path: path, Action: action}, nil
}

// kilocodeFirstRoot is what says Kilo Code is on this machine: its own
// globalStorage directory, not the editor's, so a VS Code install without the
// extension is not mistaken for a Kilo one.
func kilocodeFirstRoot() string {
	for _, root := range sources.KiloRoots() {
		if _, err := os.Stat(root); err == nil {
			return root
		}
	}
	return filepath.Join(os.DevNull, "kilocode")
}
