package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// kimiPluginVersion is the version in extensions/kimi/kimi.plugin.json. Kimi
// only offers an update for a plugin it installed from its own marketplace, so
// for a repository or zip install nothing tells the user their copy is behind.
// deja knows both numbers, and doctor is where someone looks.
const kimiPluginVersion = "0.1.0"

// kimiPluginInstalledVersion returns the version of the installed copy, or ""
// when the plugin is not there.
func kimiPluginInstalledVersion() string {
	root, ok := kimiPluginRoot()
	if !ok {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(root, "kimi.plugin.json"))
	if err != nil {
		return ""
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &manifest); err != nil {
		return ""
	}
	return manifest.Version
}

// kimiPluginInstalled reports whether the Kimi Code plugin from
// extensions/kimi is installed and enabled. It provides the same MCP server and
// the same per-prompt recall the installer writes, and stands down when the
// installer's copy is there — so a machine with only the plugin is wired, and
// doctor saying "not wired" would send someone to run an install they do not
// need.
func kimiPluginInstalled() bool {
	_, ok := kimiPluginRoot()
	return ok
}

// kimiPluginRoot finds the managed copy Kimi runs. The record lives in
// plugins/installed.json and points at the directory Kimi actually loads, so
// both have to be there.
func kimiPluginRoot() (string, bool) {
	b, err := os.ReadFile(filepath.Join(sources.KimiConfigDir(), "plugins", "installed.json"))
	if err != nil {
		return "", false
	}
	var installed struct {
		Plugins []struct {
			ID      string `json:"id"`
			Root    string `json:"root"`
			Enabled bool   `json:"enabled"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(b, &installed); err != nil {
		return "", false
	}
	for _, p := range installed.Plugins {
		if p.ID != "deja" || !p.Enabled || p.Root == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(p.Root, "kimi.plugin.json")); err == nil {
			return p.Root, true
		}
	}
	return "", false
}

// kimiPluginNote is what doctor adds after "plugin": which version is running,
// and whether this deja ships a newer one.
func kimiPluginNote() string {
	got := kimiPluginInstalledVersion()
	if got == "" {
		return ""
	}
	if got != kimiPluginVersion {
		return "v" + got + " installed, v" + kimiPluginVersion + " ships with this deja — reinstall it in Kimi to update"
	}
	return "v" + got
}
