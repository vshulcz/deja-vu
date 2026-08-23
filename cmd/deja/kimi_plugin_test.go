package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Kimi Code loads the plugin from this manifest and nothing else: a path that
// does not exist, or a field it does not know, becomes a diagnostic in
// /plugins and the capability silently never arrives.
func TestKimiPluginManifest(t *testing.T) {
	var manifest struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Skills      string `json:"skills"`
		Commands    string `json:"commands"`
		SessionStrt struct {
			Skill string `json:"skill"`
		} `json:"sessionStart"`
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
		Hooks []struct {
			Event   string `json:"event"`
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(repoFile(t, "extensions/kimi/kimi.plugin.json"), &manifest); err != nil {
		t.Fatalf("kimi.plugin.json: %v", err)
	}

	// The name is the plugin id, and Kimi rejects anything else.
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`).MatchString(manifest.Name) {
		t.Fatalf("plugin id %q is not a name Kimi accepts", manifest.Name)
	}
	if manifest.Version == "" {
		t.Fatal("no version: the marketplace and the update check both read it")
	}

	// UserPromptSubmit is the only event whose output Kimi appends to the
	// turn. A SessionStart hook runs and its answer goes nowhere.
	if len(manifest.Hooks) != 1 || manifest.Hooks[0].Event != "UserPromptSubmit" {
		t.Fatalf("recall has to hang off UserPromptSubmit, got %+v", manifest.Hooks)
	}
	if manifest.Hooks[0].Timeout < 1 || manifest.Hooks[0].Timeout > 600 {
		t.Fatalf("timeout %d is outside the 1-600s Kimi allows", manifest.Hooks[0].Timeout)
	}

	// `command: "node"` is the one form Kimi rewrites to its own runtime when
	// it ships as a native binary, so the server starts on a machine with no
	// node on PATH.
	server, ok := manifest.MCPServers["deja"]
	if !ok || server.Command != "node" || len(server.Args) != 1 {
		t.Fatalf("mcpServers.deja should run node with one script, got %+v", manifest.MCPServers)
	}

	if manifest.SessionStrt.Skill != "deja-history" {
		t.Fatalf("sessionStart.skill %q does not name the bundled skill", manifest.SessionStrt.Skill)
	}

	for _, rel := range []string{
		strings.TrimPrefix(server.Args[0], "./"),
		strings.TrimPrefix(strings.Fields(manifest.Hooks[0].Command)[1], "./"),
		strings.TrimPrefix(manifest.Skills, "./"),
		strings.TrimPrefix(manifest.Commands, "./"),
		"skills/deja-history/SKILL.md",
		"commands/recall.md",
	} {
		if _, err := os.Stat(filepath.Join("..", "..", "extensions", "kimi", rel)); err != nil {
			t.Fatalf("manifest points at %s, which is not in the plugin: %v", rel, err)
		}
	}
}

// The plugin stands down by looking for the comment the installer writes above
// its own hook. If either side edits that string alone, the two stop
// recognising each other and the user reads the same recall twice.
func TestKimiPluginKnowsTheInstallersMarker(t *testing.T) {
	lib := string(repoFile(t, "extensions/kimi/lib.mjs"))
	if !strings.Contains(lib, kimiHookMarker) {
		t.Fatalf("extensions/kimi/lib.mjs does not carry the marker the installer writes:\n%s", kimiHookMarker)
	}
}

// The version doctor compares against has to be the version the plugin ships,
// and the manifest a GitHub install reads has to be the same plugin as the one
// in the zip — only its paths differ.
func TestKimiManifestsAgree(t *testing.T) {
	var plugin, root map[string]any
	if err := json.Unmarshal(repoFile(t, "extensions/kimi/kimi.plugin.json"), &plugin); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(repoFile(t, "kimi.plugin.json"), &root); err != nil {
		t.Fatal(err)
	}
	if plugin["version"] != kimiPluginVersion {
		t.Fatalf("kimiPluginVersion is %q, the manifest says %v", kimiPluginVersion, plugin["version"])
	}
	for _, key := range []string{"name", "version", "description", "license", "sessionStart"} {
		a, _ := json.Marshal(plugin[key])
		b, _ := json.Marshal(root[key])
		if string(a) != string(b) {
			t.Fatalf("%s differs between the two manifests: %s vs %s", key, a, b)
		}
	}
	// The root manifest addresses the same files from one directory up.
	if root["skills"] != "./extensions/kimi/skills/" || root["commands"] != "./extensions/kimi/commands/" {
		t.Fatalf("root manifest does not point into extensions/kimi: %v %v", root["skills"], root["commands"])
	}
	for _, rel := range []string{"extensions/kimi/hooks/recall.mjs", "extensions/kimi/bin/deja-mcp.mjs"} {
		if _, err := os.Stat(filepath.Join("..", "..", rel)); err != nil {
			t.Fatalf("root manifest points at %s, which is not there: %v", rel, err)
		}
	}
}
