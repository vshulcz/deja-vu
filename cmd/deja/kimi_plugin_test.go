package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	// Whole, not key by key: the two differ only in where they point, and a
	// list of keys to compare is a list of keys someone forgets to extend —
	// `interface` is what Kimi's /plugins browser shows, and it went unchecked
	// (#1777).
	// The rewrite below is a string replacement, so it is only honest while the
	// prefix appears in paths and nowhere else — a description mentioning it
	// would be rewritten too, and the comparison would pass on a difference.
	for _, key := range []string{"description", "homepage", "keywords", "interface", "author"} {
		if b, _ := json.Marshal(root[key]); strings.Contains(string(b), "extensions/kimi") {
			t.Fatalf("%s mentions the path prefix, which the comparison rewrites: %s", key, b)
		}
	}
	if diff := manifestDiffIgnoringPaths(plugin, root); diff != "" {
		t.Fatalf("the two manifests disagree beyond their paths: %s", diff)
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

// manifestDiffIgnoringPaths compares the two Kimi manifests after rewriting the
// root one's paths to the packaged form. Key order and number forms do not
// matter: encoding/json sorts a map's keys and normalises 1.0 to 1, so the two
// files can be formatted differently and still compare equal. The prefix is the one intended
// difference: a GitHub install reads the repo, the zip carries the plugin
// directory itself.
func manifestDiffIgnoringPaths(plugin, root map[string]any) string {
	a, err := json.Marshal(plugin)
	if err != nil {
		return err.Error()
	}
	b, err := json.Marshal(root)
	if err != nil {
		return err.Error()
	}
	want := strings.ReplaceAll(string(b), "./extensions/kimi/", "./")
	if want == string(a) {
		return ""
	}
	return diffJSONMaps(string(a), want)
}

// diffJSONMaps names the keys that differ, so a failure says what to fix rather
// than printing two manifests.
func diffJSONMaps(a, b string) string {
	var ma, mb map[string]json.RawMessage
	if json.Unmarshal([]byte(a), &ma) != nil || json.Unmarshal([]byte(b), &mb) != nil {
		return "the manifests are not both objects"
	}
	var out []string
	for k, va := range ma {
		vb, ok := mb[k]
		if !ok {
			out = append(out, k+" is only in the packaged manifest")
			continue
		}
		if string(va) != string(vb) {
			out = append(out, fmt.Sprintf("%s: packaged %s, root %s", k, clampForDiff(string(va)), clampForDiff(string(vb))))
		}
	}
	for k := range mb {
		if _, ok := ma[k]; !ok {
			out = append(out, k+" is only in the root manifest")
		}
	}
	sort.Strings(out)
	return strings.Join(out, "; ")
}

func clampForDiff(s string) string {
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}
