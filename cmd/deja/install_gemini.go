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
		// hooksConfig stays: other extensions may rely on it, and turning it
		// off would silently disable them. Said aloud, because the rest of
		// this uninstall names what it keeps — "guidance kept …" — so silence
		// here reads as "nothing of deja's is left in settings.json", and a
		// switch deja turned on is (#2487).
		note := ""
		if geminiHooksEnabled() {
			note = "left hooksConfig.enabled on in gemini's settings.json — other extensions may be running on it"
		}
		if _, err := os.Stat(dir); err != nil {
			return installResult{Path: dir, Action: "unchanged", Note: note}, nil
		}
		if err := os.RemoveAll(dir); err != nil {
			return installResult{}, err
		}
		return installResult{Path: dir, Action: "removed", Note: note}, nil
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
	oldManifest, err := readConfig(manifestPath)
	if err != nil {
		return installResult{}, err
	}
	if _, err := writeIfChanged(manifestPath, oldManifest, append(manifest, '\n')); err != nil {
		return installResult{}, err
	}
	hooks, err := json.MarshalIndent(map[string]any{
		"hooks": map[string]any{
			// SessionStart injects additionalContext into the session history
			// and prints systemMessage.
			"SessionStart": []any{map[string]any{
				"hooks": []any{map[string]any{
					"type": "command", "command": exe + " hook-context",
					// Gemini reads timeout in milliseconds; a Claude-style 10
					// kills the hook before it can answer.
					"timeout": 10000,
				}},
			}},
			// BeforeAgent is gemini's name for UserPromptSubmit — it is handed
			// the prompt and appends what the hook returns to the request as
			// <hook_context>. Only systemMessage is limited to the blocking
			// case; additionalContext is not.
			"BeforeAgent": []any{map[string]any{
				"hooks": []any{map[string]any{
					"type": "command", "command": exe + " hook-prompt",
					"timeout": 10000,
				}},
			}},
		},
	}, "", "  ")
	if err != nil {
		return installResult{}, err
	}
	hooksPath := filepath.Join(dir, "hooks", "hooks.json")
	oldHooks, err := readConfig(hooksPath)
	if err != nil {
		return installResult{}, err
	}
	a, err := writeIfChanged(hooksPath, oldHooks, append(hooks, '\n'))
	if err != nil {
		return installResult{}, err
	}
	if err := enableGeminiHooks(); err != nil {
		return installResult{}, err
	}
	return installResult{Path: dir, Action: a}, nil
}

// geminiHooksEnabled reports whether the master switch is on right now, which
// is all an uninstall can honestly say about it: deja cannot tell its own flip
// from one the reader made before ever installing.
func geminiHooksEnabled() bool {
	b, err := os.ReadFile(filepath.Join(sources.GeminiHome(), "settings.json"))
	if err != nil {
		return false
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
		return false
	}
	cfg, _ := root["hooksConfig"].(map[string]any)
	enabled, _ := cfg["enabled"].(bool)
	return enabled
}

// enableGeminiHooks flips the master switch. Without it the extension is
// loaded and its hooks are never run.
func enableGeminiHooks() error {
	path := filepath.Join(sources.GeminiHome(), "settings.json")
	old, err := readConfig(path)
	if err != nil {
		return err
	}
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if configIsJSONC(old) {
		// The same file the MCP entry went into a moment ago. Refusing it here
		// left the target reported as refused with half its wiring written and
		// a .bak beside it (#2744).
		return enableGeminiHooksJSONC(path, old)
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
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return err
	}
	_, err = writeIfChanged(path, old, append(next, '\n'))
	return err
}

// enableGeminiHooksJSONC is enableGeminiHooks for a settings file carrying
// comments: the same decision, read from the file with its comments blanked,
// and the switch written by text so everything else stays put.
func enableGeminiHooksJSONC(path string, old []byte) error {
	var root map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(string(old))), &root); err != nil {
		return fmt.Errorf("gemini settings: %w", err)
	}
	cfg, _ := root["hooksConfig"].(map[string]any)
	if enabled, ok := cfg["enabled"].(bool); ok && enabled {
		return nil
	}
	// A key holding something else — a list, a string, null. zedFindKey does
	// not match those either, so the write would fall through to inserting a
	// second `hooksConfig` and the reader's value would win (#2745, the shape
	// #2740 closed for entries).
	if v, present := root["hooksConfig"]; present && cfg == nil {
		_ = v
		return fmt.Errorf("gemini settings: %q is not an object deja can edit — left as it was", "hooksConfig")
	}
	if v, present := cfg["enabled"]; present {
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("gemini settings: %q is not a switch deja can turn on — left as it was", "hooksConfig.enabled")
		}
	}
	next, err := jsoncSetFlag(string(old), "hooksConfig", "enabled", true)
	if err != nil {
		return fmt.Errorf("gemini settings: %w", err)
	}
	_, err = writeIfChanged(path, old, []byte(next))
	return err
}
