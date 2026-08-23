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
