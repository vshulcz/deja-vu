package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Gemini loads hooks from extensions, not from settings.json, and only when
// hooksConfig.enabled is set. A `hooks` block in settings.json — where deja
// used to write one — is read by nothing (checked on 0.52.0, headless and in
// the TUI).
//
// The extension goes in by hand rather than through `gemini extensions link`:
// that command prompts for workspace trust on a TTY, which an installer has no
// business answering. A directory under ~/.gemini/extensions is picked up on
// the next run either way.
const geminiExtensionName = "deja"

func installGeminiExtension(exe string, uninstall bool) (installResult, error) {
	dir := filepath.Join(sources.GeminiHome(), "extensions", geminiExtensionName)
	if uninstall {
		if _, err := os.Stat(dir); err != nil {
			return installResult{Path: dir, Action: "unchanged"}, nil
		}
		if err := os.RemoveAll(dir); err != nil {
			return installResult{}, err
		}
		// hooksConfig stays: other extensions may rely on it, and turning it
		// off would silently disable them.
		return installResult{Path: dir, Action: "removed"}, nil
	}
	if err := os.MkdirAll(filepath.Join(dir, "hooks"), 0o755); err != nil {
		return installResult{}, err
	}
	manifest, err := json.MarshalIndent(map[string]any{
		"name":        geminiExtensionName,
		"version":     "0.1.0",
		"description": "Recall your own past coding sessions before you ask.",
	}, "", "  ")
	if err != nil {
		return installResult{}, err
	}
	manifestPath := filepath.Join(dir, "gemini-extension.json")
	oldManifest, _ := os.ReadFile(manifestPath)
	if _, err := writeIfChanged(manifestPath, oldManifest, append(manifest, '\n')); err != nil {
		return installResult{}, err
	}
	hooks, err := json.MarshalIndent(map[string]any{
		"hooks": map[string]any{
			// SessionStart, not BeforeAgent: it injects additionalContext into
			// the session history and prints systemMessage, while BeforeAgent
			// surfaces the message only when the hook blocks execution.
			"SessionStart": []any{map[string]any{
				"hooks": []any{map[string]any{
					"type": "command", "command": exe + " hook-context",
					// Gemini reads timeout in milliseconds; a Claude-style 10
					// kills the hook before it can answer.
					"timeout": 10000,
				}},
			}},
		},
	}, "", "  ")
	if err != nil {
		return installResult{}, err
	}
	hooksPath := filepath.Join(dir, "hooks", "hooks.json")
	oldHooks, _ := os.ReadFile(hooksPath)
	a, err := writeIfChanged(hooksPath, oldHooks, append(hooks, '\n'))
	if err != nil {
		return installResult{}, err
	}
	if err := enableGeminiHooks(); err != nil {
		return installResult{}, err
	}
	return installResult{Path: dir, Action: a}, nil
}

// enableGeminiHooks flips the master switch. Without it the extension is
// loaded and its hooks are never run.
func enableGeminiHooks() error {
	path := filepath.Join(sources.GeminiHome(), "settings.json")
	old, _ := os.ReadFile(path)
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return fmt.Errorf("gemini settings: %w", err)
	}
	cfg, _ := root["hooksConfig"].(map[string]any)
	if cfg == nil {
		cfg = map[string]any{}
		root["hooksConfig"] = cfg
	}
	if enabled, ok := cfg["enabled"].(bool); ok && enabled {
		return nil
	}
	cfg["enabled"] = true
	next, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	_, err = writeIfChanged(path, old, append(next, '\n'))
	return err
}
