package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// grokPluginVersion is the version in extensions/grok/.grok-plugin/plugin.json.
//
// Grok installs the plugin from a marketplace entry that pins a commit, so an
// installed copy stays at whatever the pin was until someone moves it and
// nothing on the machine says the copy is old: the bundle went to 0.2.0 in
// #1721 while every installed copy was still 0.1.0. deja knows both numbers and
// doctor is where someone looks, which is the argument kimiPluginVersion
// already makes for Kimi (#1828).
const grokPluginVersion = "0.2.0"

// grokPluginRoot finds the installed copy Grok runs, by its manifest rather
// than by its path: the directory under installed-plugins is generated, so
// there is nothing fixed to join onto. A directory whose manifest names another
// plugin is somebody else's.
func grokPluginRoot() (string, bool) {
	matches, err := filepath.Glob(filepath.Join(sources.GrokHome(), "installed-plugins", "*"))
	if err != nil {
		return "", false
	}
	for _, dir := range matches {
		if name, _, ok := grokPluginManifest(dir); ok && name == "deja" {
			return dir, true
		}
	}
	return "", false
}

// grokPluginManifest reads a plugin directory's own manifest.
func grokPluginManifest(dir string) (name, version string, ok bool) {
	b, err := os.ReadFile(filepath.Join(dir, ".grok-plugin", "plugin.json"))
	if err != nil {
		return "", "", false
	}
	var manifest struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &manifest); err != nil {
		return "", "", false
	}
	return manifest.Name, manifest.Version, true
}

// grokPluginInstalled reports whether the Grok Build plugin from
// extensions/grok is installed. It carries the same MCP server and the same
// per-prompt recall the installer writes and stands down where the installer's
// copy is there, so a machine with only the plugin is wired and doctor calling
// it "not wired" would send someone to run an install they do not need.
func grokPluginInstalled() bool {
	_, ok := grokPluginRoot()
	return ok
}

// grokPluginInstalledVersion returns the version of the installed copy, or ""
// when the plugin is not there. Not there is not an error: most machines have
// no Grok at all, and doctor has to run on them.
func grokPluginInstalledVersion() string {
	dir, ok := grokPluginRoot()
	if !ok {
		return ""
	}
	_, version, _ := grokPluginManifest(dir)
	return version
}

// grokPluginNote is what doctor adds after "plugin": which version is running,
// and whether this deja ships a newer one.
//
// Behind, never merely different. `grok plugin install ./…` installs a working
// copy, whose version may legitimately be ahead of the one this deja ships, and
// deja cannot tell that copy from a marketplace one by the files it can see —
// the installed directory holds the same manifest either way. Telling someone
// their newer plugin is stale would be wrong where saying nothing is only
// quiet, so a copy at or above this version just reports its number. A version
// neither side can parse is the same case: report it, claim nothing about it.
func grokPluginNote() string {
	got := grokPluginInstalledVersion()
	if got == "" {
		return ""
	}
	if cmp, ok := compareUpdateVersions(got, grokPluginVersion); ok && cmp < 0 {
		return "v" + got + " installed, v" + grokPluginVersion + " ships with this deja — `grok plugin update deja`"
	}
	return "v" + got
}
