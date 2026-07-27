package main

import (
	"os"
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Roo has no lifecycle hooks — the only "hooks" in its bundle are React ones.
// What it has is an MCP settings file per VS Code host, and a global rules
// directory read into every task regardless of mode.
//
// Rules are the closest thing to session-start injection Roo offers. They are
// static, and nothing in Roo can refresh them mid-session, so what goes there
// is the guidance block rather than a digest that would rot.
func rooMCPSettingsPaths() []string {
	var out []string
	for _, root := range sources.RooRoots() {
		out = append(out, filepath.Join(root, "settings", "mcp_settings.json"))
	}
	return out
}

// installRoo writes the MCP server into every host that has Roo installed:
// the same person often runs it in both VS Code and Cursor, and wiring one
// leaves the other silently without recall.
func installRoo(exe string, uninstall bool) (installResult, error) {
	paths := rooMCPSettingsPaths()
	var last installResult
	wrote := false
	for _, p := range paths {
		// Only hosts Roo has actually run in: creating the directory would
		// leave settings behind for an editor that is not installed.
		if _, err := os.Stat(filepath.Dir(filepath.Dir(p))); err != nil {
			continue
		}
		res, err := installMCPJSON(p, exe, uninstall)
		if err != nil {
			return installResult{}, err
		}
		last = res
		if res.Action != "unchanged" {
			wrote = true
		}
	}
	if !wrote && last.Path == "" {
		return installResult{Path: "roo", Action: "unchanged"}, nil
	}
	return last, nil
}

// rooRulesPath is the global rules directory: <home>/.roo/rules, read for
// every mode. Mode-specific directories (rules-code, rules-ask, …) sit beside
// it and would each need their own copy.
func rooRulesPath() string {
	return filepath.Join(homeDir(), ".roo", "rules", "deja.md")
}

// rooFirstRoot is a path that exists only when Roo has run in one of the
// hosts, so detection does not fire on a machine that merely has VS Code.
func rooFirstRoot() string {
	for _, root := range sources.RooRoots() {
		if _, err := os.Stat(root); err == nil {
			return root
		}
	}
	return filepath.Join(os.DevNull, "roo")
}
