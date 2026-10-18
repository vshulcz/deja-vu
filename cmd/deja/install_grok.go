package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
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
	path, old, next, note, err := grokUserSettingsUpdate(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	if next == nil {
		return installResult{Path: path, Action: "unchanged"}, nil
	}
	noteCreatedDirs(filepath.Dir(path))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return installResult{}, err
	}
	a, werr := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a, Note: note}, werr
}

// grokUserSettingsUpdate is installGrokUserSettings without the write: next
// is nil when there is nothing to write.
func grokUserSettingsUpdate(exe string, uninstall bool) (path string, old, next []byte, note string, err error) {
	path = filepath.Join(sources.GrokHome(), "user-settings.json")
	old, err = readConfig(path)
	if err != nil {
		return path, nil, nil, "", err
	}
	// Round-tripping through a map keeps every key this CLI version has that
	// deja knows nothing about.
	root := map[string]any{}
	if len(old) > 0 {
		if err := json.Unmarshal(old, &root); err != nil {
			return path, old, nil, "", configParseError(path, err)
		}
	}
	mcp, _ := root["mcp"].(map[string]any)
	if mcp == nil {
		mcp = map[string]any{}
	}
	var kept []any
	// grok-dev's MCP modal switches a server off with "enabled": false, and
	// its runtime skips the entry. The rebuilt entry said true (#5196).
	// The entry grok-dev keys by id "deja" decides, then one labelled deja.
	off, byID := false, false
	if servers, ok := mcp["servers"].([]any); ok {
		for _, s := range servers {
			if m, ok := s.(map[string]any); ok {
				if m["id"] == "deja" || m["label"] == "deja" {
					if id := m["id"] == "deja"; id || !byID {
						off = m["enabled"] == false
						byID = byID || id
					}
					continue
				}
			}
			kept = append(kept, s)
		}
	}
	// Another entry that runs deja is said, not doubled in silence (#4556).
	if !uninstall {
		var others []string
		for _, s := range kept {
			if m, ok := s.(map[string]any); ok && entryIsDejaServer(m) {
				if id, _ := m["id"].(string); id != "" {
					others = append(others, id)
				}
			}
		}
		if off {
			note = switchedOffNote
		}
		note = withOtherDejaNames(note, others)
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
			"enabled":   !off,
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
	if uninstall && len(old) == 0 {
		return path, old, nil, "", nil
	}
	next, err = marshalConfigLike(old, root)
	if err != nil {
		return path, old, nil, "", err
	}
	return path, old, append(next, '\n'), note, nil
}

// grokPreflight refuses, before config.toml is written, a later grok file
// install would need to change and cannot: config.toml goes first, and a
// read-only user-settings.json or hooks directory was refused once it already
// held deja (#5197).
func grokPreflight(exe string, auto bool) error {
	path, old, next, _, err := grokUserSettingsUpdate(exe, false)
	if err != nil {
		return err
	}
	if err := writeWouldFail(path, old, next); err != nil {
		return err
	}
	if !auto {
		return nil
	}
	path, old, next, _, err = grokHooksUpdate(preflightHookExe(exe), false)
	if err != nil {
		return err
	}
	return writeWouldFail(path, old, next)
}

// preflightHookExe is the path a hook writer will name, without writing the
// launcher yet: the binary itself when writeDejaLauncher would fail. That
// rewrites an existing launcher in place, so only the file has to be
// writable then, not its directory.
func preflightHookExe(exe string) string {
	p := dejaLauncherPath()
	if p == "" {
		return exe
	}
	next := []byte(dejaLauncherScript(exe))
	if cur, err := os.ReadFile(p); err == nil {
		if bytes.Equal(cur, next) || configWritable(p) == nil {
			return p
		}
		return exe
	}
	if writeWouldFail(p, nil, next) != nil {
		return exe
	}
	return p
}

// writeWouldFail says why writing next over old at path would be refused:
// the file is read-only, or the directory the write goes through (and the
// .bak beside it) is.
func writeWouldFail(path string, old, next []byte) error {
	if next == nil || bytes.Equal(old, matchFinalNewline(old, matchLineEndings(old, next))) {
		return nil
	}
	if err := configWritable(path); err != nil {
		return err
	}
	// writeIfChanged follows a link into the dotfiles repo, and its temp file
	// and .bak go beside the target.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	dir := filepath.Dir(path)
	for {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
	if !dirWritable(dir) {
		return &fs.PathError{Op: "write", Path: dir, Err: fs.ErrPermission}
	}
	return nil
}
