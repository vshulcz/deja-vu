package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	var skipped []string
	wrote := false
	seen := 0
	for _, p := range paths {
		// Only hosts Roo has actually run in: creating the directory would
		// leave settings behind for an editor that is not installed.
		if _, err := os.Stat(filepath.Dir(filepath.Dir(p))); err != nil {
			continue
		}
		seen++
		err := mcpEntryWritable(p, "mcpServers")
		if err == nil && !uninstall {
			// Reading the settings is not enough: the snapshot and the write
			// both land in the host's settings directory, so a directory deja
			// cannot write is the same half-install shape one host later.
			if dir := firstDirThatIsThere(filepath.Dir(p)); !dirWritable(dir) {
				err = fmt.Errorf("%s: deja cannot write in that directory — check its permissions", shortHome(dir))
			}
		}
		if err == nil {
			continue
		}
		// One settings file per host, written in turn: a refusal on the second
		// host used to leave the first one wired with a .bak beside it while
		// the run reported the target refused (#2750). Every host is asked
		// before any is written.
		if !uninstall {
			return installResult{}, err
		}
		// On the way out, a host deja cannot read is one it cannot take its
		// entry out of — and refusing there would leave the hosts it can read
		// wired, which is what the run was asked to undo. Named, so the reader
		// knows which one still has deja in it.
		skipped = append(skipped, shortHome(p))
	}
	for _, p := range paths {
		if _, err := os.Stat(filepath.Dir(filepath.Dir(p))); err != nil {
			continue
		}
		if uninstall && mcpEntryWritable(p, "mcpServers") != nil {
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
	if len(skipped) > 0 {
		// A host that was there and could not be read is not "no host found":
		// deja is still wired in it, and saying nothing would leave the reader
		// thinking the uninstall was complete.
		note := "left " + strings.Join(skipped, ", ") + " — deja could not read " + pluralWhich(len(skipped))
		if last.Path == "" {
			return installResult{Action: note}, nil
		}
		last.Note = joinNotes(last.Note, note)
		return last, nil
	}
	if !wrote && last.Path == "" && seen == 0 {
		// No host has ever run Roo here, so there is no settings file to write
		// into and creating one would leave configuration for an editor that is
		// not installed. Say that, rather than "unchanged roo", which names a
		// path that does not exist and explains nothing.
		return installResult{Action: "no Roo host found — open Roo in VS Code once, then re-run"}, nil
	}
	return last, nil
}

// rooRulesPath is where deja used to write guidance: the global rules
// directory <home>/.roo/rules, read verbatim into the system prompt for every
// mode and every task. Guidance is a skill now and install removes this file;
// the path survives so it can be cleaned up on machines that still have it.
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
