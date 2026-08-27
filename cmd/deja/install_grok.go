package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Three shipping CLIs share ~/.grok and none of them read the same file:
//
//   - Grok Build (xai-org) reads [mcp_servers.<name>] from config.toml.
//   - grok-dev reads mcp.servers out of user-settings.json, an array of
//     objects with id/label/enabled rather than a map.
//   - @vibe-kit/grok-cli reads mcpServers only from <cwd>/.grok/settings.json;
//     it has no user-level MCP at all, so nothing an installer writes to the
//     home directory can reach it.
//
// deja used to write config.toml alone, which left grok-dev users with an
// integration that looked installed and did nothing.
func installGrokUserSettings(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.GrokHome(), "user-settings.json")
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return installResult{}, err
	}
	// Round-tripping through a map keeps every key this CLI version has that
	// deja knows nothing about.
	root := map[string]any{}
	if len(old) > 0 {
		if err := json.Unmarshal(old, &root); err != nil {
			return installResult{}, configParseError(path, err)
		}
	}
	mcp, _ := root["mcp"].(map[string]any)
	if mcp == nil {
		mcp = map[string]any{}
	}
	var kept []any
	if servers, ok := mcp["servers"].([]any); ok {
		for _, s := range servers {
			if m, ok := s.(map[string]any); ok {
				if m["id"] == "deja" || m["label"] == "deja" {
					continue
				}
			}
			kept = append(kept, s)
		}
	}
	if !uninstall {
		cmd, args := mcpCommandArgs(exe)
		anyArgs := make([]any, 0, len(args))
		for _, a := range args {
			anyArgs = append(anyArgs, a)
		}
		kept = append(kept, map[string]any{
			"id":        "deja",
			"label":     "deja",
			"enabled":   true,
			"transport": "stdio",
			"command":   cmd,
			"args":      anyArgs,
		})
	}
	if len(kept) == 0 {
		delete(mcp, "servers")
	} else {
		mcp["servers"] = kept
	}
	if len(mcp) == 0 {
		delete(root, "mcp")
	} else {
		root["mcp"] = mcp
	}
	next, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	if uninstall && len(old) == 0 {
		return installResult{Path: path, Action: "unchanged"}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return installResult{}, err
	}
	a, werr := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, werr
}
