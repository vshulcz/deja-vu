package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The Agent Plugins manifest schema is closed: an unknown top-level field is a
// schema violation, and the name has its own grammar. Both are the kind of thing
// a directory rejects on submission rather than a reviewer catching by eye, so
// they are pinned here.
//
// https://open-plugins.com/plugin-authors/manifest
func TestAgentPluginManifestIsPortable(t *testing.T) {
	for _, path := range []string{"plugin.json", "claude-plugin/plugin.json"} {
		t.Run(path, func(t *testing.T) { checkAgentPluginManifest(t, path) })
	}
}

// The Claude Code plugin carries its manifest twice: .claude-plugin/plugin.json
// for Claude Code and plugin.json at the plugin root for directories that read
// the Agent Plugins layout (awesome-copilot's intake rejected the plugin for
// its absence, and then for the version being behind the tag). One version.
func TestClaudePluginManifestsAgree(t *testing.T) {
	var a, b map[string]any
	if err := json.Unmarshal(repoFile(t, "claude-plugin/.claude-plugin/plugin.json"), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(repoFile(t, "claude-plugin/plugin.json"), &b); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"name", "version", "description", "license"} {
		if a[k] != b[k] {
			t.Errorf("%s: .claude-plugin has %v, plugin.json has %v", k, a[k], b[k])
		}
	}
	for _, m := range []map[string]any{a, b} {
		kws, _ := m["keywords"].([]any)
		for _, kw := range kws {
			if s, _ := kw.(string); !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(s) {
				t.Errorf("keyword %q: directories accept lowercase letters, digits and hyphens only", s)
			}
		}
	}
}

func checkAgentPluginManifest(t *testing.T, path string) {
	t.Helper()
	var manifest map[string]any
	if err := json.Unmarshal(repoFile(t, path), &manifest); err != nil {
		t.Fatalf("%s: %v", path, err)
	}

	allowed := map[string]bool{
		"$schema": true, "name": true, "version": true, "description": true,
		"author": true, "homepage": true, "repository": true, "license": true,
		"keywords": true, "extensions": true,
	}
	for key := range manifest {
		if !allowed[key] {
			t.Errorf("%q is not a portable top-level field; client-specific data belongs under extensions", key)
		}
	}
	for _, required := range []string{"$schema", "name"} {
		if manifest[required] == nil {
			t.Errorf("%q is required", required)
		}
	}

	// 1–64 characters, lowercase ASCII letters, digits, hyphens and periods,
	// starting and ending alphanumeric, with no doubled separator.
	name, _ := manifest["name"].(string)
	ok := regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,62}[a-z0-9])?$`).MatchString(name)
	if !ok || regexp.MustCompile(`--|\.\.`).MatchString(name) {
		t.Errorf("plugin name %q does not satisfy the Agent Plugins grammar", name)
	}
}

// Skills are discovered at a fixed depth: an immediate child of skills/ whose
// SKILL.md is a regular file. A skill nested one level deeper is not found, and
// nothing else in this repository would notice.
func TestAgentPluginSkillsAreDiscoverable(t *testing.T) {
	root := filepath.Join("..", "..")
	entries, err := os.ReadDir(filepath.Join(root, "skills"))
	if err != nil {
		t.Fatalf("skills/: %v", err)
	}
	found := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := os.Stat(filepath.Join(root, "skills", e.Name(), "SKILL.md"))
		if err != nil || !info.Mode().IsRegular() {
			t.Errorf("skills/%s has no SKILL.md at the top level, so no client will discover it", e.Name())
			continue
		}
		found++
	}
	if found == 0 {
		t.Fatal("no skill is discoverable, which makes the plugin manifest a promise of nothing")
	}
}

// mcp.json is a closed document too: only $schema and mcpServers, and a stdio
// command is one executable token rather than a shell line.
func TestAgentPluginMCPDocument(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(repoFile(t, "mcp.json"), &doc); err != nil {
		t.Fatalf("mcp.json: %v", err)
	}
	for key := range doc {
		if key != "$schema" && key != "mcpServers" {
			t.Errorf("%q is not allowed at the top level of mcp.json", key)
		}
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	server, ok := servers["deja"].(map[string]any)
	if !ok {
		t.Fatal("mcp.json declares no deja server")
	}
	if server["type"] != "stdio" {
		t.Errorf("transport is %v, want stdio", server["type"])
	}
	cmd, _ := server["command"].(string)
	if cmd != "deja" {
		t.Errorf("command is %q; it has to be one executable token resolved on PATH, not a shell command", cmd)
	}
}
