package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Gemini loads hooks from extensions, not from settings.json, and none at all
// with hooksConfig.enabled false (the key defaults to true on 0.60). A `hooks` block in settings.json — where deja
// used to write one — is read by nothing (checked on 0.52.0, headless and in
// the TUI).
//
// The extension goes in by hand rather than through `gemini extensions link`:
// that command prompts for workspace trust on a TTY, which an installer has no
// business answering. A directory under ~/.gemini/extensions is picked up on
// the next run either way.
const geminiExtensionName = "deja"

func installGeminiExtension(exe string, uninstall bool) (installResult, error) {
	exe = hookExeFor(exe, uninstall)
	dir := filepath.Join(sources.GeminiHome(), "extensions", geminiExtensionName)
	if uninstall {
		// hooksConfig stays: other extensions may rely on it, and turning it
		// off would silently disable them. Said aloud, because the rest of
		// this uninstall names what it keeps — "guidance kept …" — so silence
		// here reads as "nothing of deja's is left in settings.json", and a
		// switch deja turned on is (#2487).
		_, statErr := os.Stat(dir)
		if statErr == nil {
			if err := os.RemoveAll(dir); err != nil {
				return installResult{}, err
			}
			// And the directories deja made above it, while each is empty
			// and the record says it is deja's (#3698).
			pruneCreatedDir(filepath.Dir(dir))
		}
		// Unless deja added the switch itself and no extension left has
		// hooks to run on it: then it goes too, and the file comes back as
		// it was (#4216).
		if err := disableGeminiHooksIfOurs(); err != nil {
			return installResult{}, err
		}
		if err := restoreGeminiHooksSwitch(); err != nil {
			return installResult{}, err
		}
		note := ""
		if geminiHooksEnabled() {
			note = "left hooksConfig.enabled on in gemini's settings.json — other extensions may be running on it"
		}
		if statErr != nil {
			return installResult{Path: dir, Action: "unchanged", Note: note}, nil
		}
		return installResult{Path: dir, Action: "removed", Note: note}, nil
	}
	noteCreatedDirs(filepath.Join(dir, "hooks"))
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
					"type": "command", "command": hookRun(exe, "hook-context"),
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
					"type": "command", "command": hookRun(exe, "hook-prompt"),
					"timeout": 10000,
				}},
			}},
			// The fix pair, arriving where a failing command is read: what this
			// hook returns is appended to the tool result as <hook_context>.
			// Checked on gemini-cli 0.55.1, and BeforeTool is not the pair to
			// it — that one fires, but what it returns decides whether the tool
			// runs and never reaches the model, so wiring it would cost a
			// process per command and inject nothing.
			//
			// Matched on the tool that runs a command; gemini honours the
			// matcher, so this never spawns on a read or a glob.
			"AfterTool": []any{map[string]any{
				"matcher": "run_shell_command",
				"hooks": []any{map[string]any{
					"type": "command", "command": hookRun(exe, "hook-tool-after"),
					"timeout": 10000,
				}},
			}},
			// Fired on exit — `gemini -p` included — and on /clear, which starts
			// a new session id. Drops the session's live stamp so the next
			// session's MCP recall can answer with it (#4210).
			"SessionEnd": []any{map[string]any{
				"hooks": []any{map[string]any{
					"type": "command", "command": hookRun(exe, "hook-session-end"),
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
	note, err := enableGeminiHooks()
	if err != nil {
		return installResult{}, err
	}
	return installResult{Path: dir, Action: a, Note: note}, nil
}

// geminiSwitchRecord is the record that hooksConfig.enabled was false before
// deja turned it on, so uninstall can put it back (#4471).
const geminiSwitchRecord = "hooksConfig.enabled=false"

// restoreGeminiHooksSwitch turns hooksConfig.enabled back off when deja is
// the one that turned it on over the reader's false. Their other hooks did
// not run before deja came, and they do not run after it goes.
func restoreGeminiHooksSwitch() error {
	path := filepath.Join(sources.GeminiHome(), "settings.json")
	if !blockWasAdded(path, geminiSwitchRecord) {
		return nil
	}
	forgetBlockAdded(path, geminiSwitchRecord)
	old, err := readConfig(path)
	if err != nil {
		return err
	}
	if cfg, _ := geminiHooksConfig(old); cfg["enabled"] != true {
		return nil
	}
	next, err := jsoncSetFlag(string(old), "hooksConfig", "enabled", false)
	if err != nil {
		return fmt.Errorf("gemini settings: %w", err)
	}
	_, err = writeIfChanged(path, old, []byte(next))
	return err
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
	if err := json.Unmarshal([]byte(jsoncToJSON(string(b))), &root); err != nil {
		return false
	}
	cfg, _ := root["hooksConfig"].(map[string]any)
	enabled, _ := cfg["enabled"].(bool)
	return enabled
}

// disableGeminiHooksIfOurs takes hooksConfig out of settings.json when deja
// added it to a file that had none and no other extension declares hooks.
func disableGeminiHooksIfOurs() error {
	path := filepath.Join(sources.GeminiHome(), "settings.json")
	if !blockWasAdded(path, "hooksConfig") {
		return nil
	}
	if geminiOtherHooks() {
		return nil
	}
	old, err := readConfig(path)
	if err != nil || len(bytes.TrimSpace(old)) == 0 {
		return err
	}
	var root map[string]any
	if json.Unmarshal([]byte(jsoncToJSON(string(old))), &root) != nil {
		return nil
	}
	// Only the switch deja wrote: a key the reader added beside it since
	// makes the object theirs as well.
	if cfg, _ := root["hooksConfig"].(map[string]any); len(cfg) != 1 || cfg["enabled"] != true {
		forgetBlockAdded(path, "hooksConfig")
		return nil
	}
	// Hooks the reader keeps in settings.json itself run on the switch too.
	if _, ok := root["hooks"]; ok {
		return nil
	}
	text := string(old)
	open := zedTopLevelOpen(text)
	if open < 0 {
		return nil
	}
	found := zedFindKey(text, open+1, "hooksConfig")
	if found == nil {
		return nil
	}
	cut := zedEntrySpan(text, found)
	next := text[:cut[0]] + text[cut[1]:]
	// No comma right behind the value: the last key, whose comma is the one in
	// front of it, or a hand-edited file with the comma further on, behind a
	// newline or a comment. Gemini parses settings.json strictly once comments
	// are stripped, so a comma left over breaks the file.
	if !strings.Contains(text[found.valueEnd:cut[1]], ",") {
		blank := stripJSONComments(text)
		i := cut[0] - 1
		for i >= 0 && strings.ContainsRune(" \t\r\n", rune(blank[i])) {
			i--
		}
		j := cut[1]
		for j < len(blank) && strings.ContainsRune(" \t\r\n", rune(blank[j])) {
			j++
		}
		switch {
		case i >= 0 && blank[i] == ',':
			next = text[:i] + text[i+1:cut[0]] + text[cut[1]:]
		case j < len(blank) && blank[j] == ',':
			next = text[:cut[0]] + text[cut[1]:j] + text[j+1:]
		}
	}
	if _, err := writeIfChanged(path, old, []byte(next)); err != nil {
		return err
	}
	forgetBlockAdded(path, "hooksConfig")
	return nil
}

// geminiOtherHooks reports whether anything besides deja's extension runs on
// the switch: another extension with hooks/hooks.json, or a linked extension,
// whose hooks live at its source and are not visible from here.
func geminiOtherHooks() bool {
	exts := filepath.Join(sources.GeminiHome(), "extensions")
	if m, _ := filepath.Glob(filepath.Join(exts, "*", "hooks", "hooks.json")); len(m) > 0 {
		return true
	}
	metas, _ := filepath.Glob(filepath.Join(exts, "*", ".gemini-extension-install.json"))
	for _, p := range metas {
		if b, err := os.ReadFile(p); err == nil && bytes.Contains(b, []byte(`"link"`)) {
			return true
		}
	}
	return false
}

// enableGeminiHooks flips the master switch. Without it the extension is
// loaded and its hooks are never run. A false the reader set is every hook
// switched off, theirs included — the key defaults to true — so turning it on
// is said aloud and written down for uninstall to undo (#4471, the rule
// zcode's hooks.enabled follows — #4431).
func enableGeminiHooks() (string, error) {
	path := filepath.Join(sources.GeminiHome(), "settings.json")
	old, err := readConfig(path)
	if err != nil {
		return "", err
	}
	note := ""
	if cfg, _ := geminiHooksConfig(old); cfg["enabled"] == false {
		noteBlockAdded(path, geminiSwitchRecord)
		note = "turned hooksConfig.enabled on in " + shortHome(path) + ", which was off, so its other hooks run too; uninstall turns it back off"
	}
	err = setGeminiHooksOn(path, old)
	return note, err
}

func setGeminiHooksOn(path string, old []byte) error {
	if cfg, present := geminiHooksConfig(old); present && (len(cfg) != 1 || cfg["enabled"] != true) {
		// The reader's own object — a switch they set to false, a key
		// beside it: not deja's to take back later.
		forgetBlockAdded(path, "hooksConfig")
	}
	if !geminiHasHooksConfig(old) {
		// Recorded before the write, which is the only moment the absence
		// can be seen; an uninstall then knows the object is deja's.
		defer func() {
			if now, _ := readConfig(path); geminiHasHooksConfig(now) {
				noteBlockAdded(path, "hooksConfig")
			}
		}()
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
	if err := json.Unmarshal([]byte(jsoncToJSON(string(old))), &root); err != nil {
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

// geminiHasHooksConfig reports whether settings.json carries a hooksConfig key.
func geminiHasHooksConfig(b []byte) bool {
	var root map[string]any
	if json.Unmarshal([]byte(jsoncToJSON(string(b))), &root) != nil {
		return false
	}
	_, ok := root["hooksConfig"]
	return ok
}

// geminiHooksConfig is settings.json's hooksConfig object and whether the key
// is there at all.
func geminiHooksConfig(b []byte) (map[string]any, bool) {
	var root map[string]any
	if json.Unmarshal([]byte(jsoncToJSON(string(b))), &root) != nil {
		return nil, false
	}
	v, ok := root["hooksConfig"]
	cfg, _ := v.(map[string]any)
	return cfg, ok
}
