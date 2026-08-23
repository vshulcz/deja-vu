package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// kimiPluginInstalled reports whether the Kimi Code plugin from
// extensions/kimi is installed and enabled. It provides the same MCP server and
// the same per-prompt recall the installer writes, and stands down when the
// installer's copy is there — so a machine with only the plugin is wired, and
// doctor saying "not wired" would send someone to run an install they do not
// need.
//
// Kimi keeps the record in plugins/installed.json and runs the plugin from the
// managed copy that record points at, so both have to be there.
func kimiPluginInstalled() bool {
	b, err := os.ReadFile(filepath.Join(sources.KimiConfigDir(), "plugins", "installed.json"))
	if err != nil {
		return false
	}
	var installed struct {
		Plugins []struct {
			ID      string `json:"id"`
			Root    string `json:"root"`
			Enabled bool   `json:"enabled"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(b, &installed); err != nil {
		return false
	}
	for _, p := range installed.Plugins {
		if p.ID != "deja" || !p.Enabled || p.Root == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(p.Root, "kimi.plugin.json")); err == nil {
			return true
		}
	}
	return false
}
