package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Antigravity takes customizations as plugins: a directory under a
// customization root's plugins/ folder, marked by plugin.json. Everything
// beside that marker — hooks.json, mcp_config.json, skills/, rules/ — is
// ingested automatically.
//
// Three details, all found by running it rather than reading the binary:
//   - The root is the same ~/.gemini/config that holds mcp_config.json. The
//     docs name `.agents/` as a customization root, but a probe placed there
//     never fired.
//   - Without plugin.json the directory is not a plugin and hooks.json is
//     ignored silently.
//   - hooks.json is a map of named hook to events, not a flat list, and
//     PreInvocation takes handlers directly while PreToolUse wraps them in a
//     matcher group.
//
// PreInvocation runs before each model call and injects whatever steps the
// hook prints, which is how the digest gets in.
const antigravityPluginName = "deja"

func installAntigravityPlugin(exe string, uninstall bool) (installResult, error) {
	dir := filepath.Join(antigravityConfigHome(), "plugins", antigravityPluginName)
	if uninstall {
		if _, err := os.Stat(dir); err != nil {
			return installResult{Path: dir, Action: "unchanged"}, nil
		}
		if err := os.RemoveAll(dir); err != nil {
			return installResult{}, err
		}
		return installResult{Path: dir, Action: "removed"}, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return installResult{}, err
	}
	manifest, err := json.MarshalIndent(map[string]any{"name": antigravityPluginName}, "", "  ")
	if err != nil {
		return installResult{}, err
	}
	manifest = append(manifest, '\n')
	manifestPath := filepath.Join(dir, "plugin.json")
	oldManifest, _ := os.ReadFile(manifestPath)
	if _, err := writeIfChanged(manifestPath, oldManifest, manifest); err != nil {
		return installResult{}, err
	}
	hooksPath := filepath.Join(dir, "hooks.json")
	oldHooks, _ := os.ReadFile(hooksPath)
	next, err := json.MarshalIndent(map[string]any{
		"deja-recall": map[string]any{
			"PreInvocation": []any{map[string]any{
				"type":    "command",
				"command": fmt.Sprintf("%q hook-antigravity", exe),
				"timeout": 10,
			}},
		},
	}, "", "  ")
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(hooksPath, oldHooks, next)
	if err != nil {
		return installResult{}, err
	}
	return installResult{Path: dir, Action: a}, nil
}
