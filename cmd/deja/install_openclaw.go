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

// OpenClaw has its own internal hook system: a pack is a directory with
// HOOK.md (frontmatter) plus handler.js, and ~/.openclaw/hooks is a discovery
// root, so no `openclaw plugins install` step is needed.
//
// The injection point is the agent:bootstrap event, whose context carries
// bootstrapFiles — the Project Context set. Appending an entry there puts the
// digest in front of the model without writing anything to the workspace.
//
// Two things this cost an hour to learn, both by running it:
//   - A pack in hooks/ is loaded by the gateway only: `openclaw agent --local`
//     never runs it, so the pack looks dead when tested that way. The event
//     itself fires on every agent run (2026.9.8), and deja's plugin answers it
//     under --local too.
//   - Internal hooks are off wholesale until hooks.internal.enabled is set, and
//     a pack that is listed as "ready" still never runs until then.
//
// OpenClaw's default backend is claude-cli, which already inherits deja's
// Claude Code hook; this pack is what covers its other providers.
const openclawHookName = "deja-recall"

func installOpenClawHooks(exe string, uninstall bool) (installResult, error) {
	// The launcher, not this binary: a generated plugin is as much a
	// config as a hooks.json, and one that names the build it was
	// installed from stops working the day that build moves (#3682).
	exe = hookExeFor(exe, uninstall)
	dir := filepath.Join(sources.OpenClawStateDir(), "hooks", openclawHookName)
	if uninstall {
		// "removed" only when there was something to remove: a second
		// uninstall reported taking this directory again, which is a line
		// about work that did not happen (#3698, the shape #3689 fixed in
		// goose's report).
		had := isRealDir(dir)
		if err := os.RemoveAll(dir); err != nil {
			return installResult{}, err
		}
		if _, err := setOpenClawHookEnabled(false); err != nil {
			return installResult{}, err
		}
		if !had {
			return installResult{Path: dir, Action: "unchanged"}, nil
		}
		pruneCreatedDir(filepath.Dir(dir))
		return installResult{Path: dir, Action: "removed"}, nil
	}
	noteCreatedDirs(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return installResult{}, err
	}
	docPath := filepath.Join(dir, "HOOK.md")
	oldDoc, err := readConfig(docPath)
	if err != nil {
		return installResult{}, err
	}
	if _, err := writeIfChanged(docPath, oldDoc, []byte(openclawHookDoc())); err != nil {
		return installResult{}, err
	}
	handlerPath := filepath.Join(dir, "handler.js")
	oldHandler, err := readConfig(handlerPath)
	if err != nil {
		return installResult{}, err
	}
	a, err := writeIfChanged(handlerPath, oldHandler, []byte(openclawHandlerJS(exe)))
	if err != nil {
		return installResult{}, err
	}
	// `openclaw hooks disable deja-recall` is the reader's say, and so is the
	// switch above it while deja's own entry is off: neither is turned back on
	// (#4472). The switch alone being off is the one deja needs on, and it
	// says so, the way zcode's does (#4431).
	var note string
	if openclawEntrySwitchedOff(openclawHookEntries, openclawHookName) {
		note = "left deja's hook switched off, the way it was — `openclaw hooks enable " + openclawHookName + "` turns it back on"
	} else {
		if openclawHooksSwitchOff() {
			note = "turned hooks.internal.enabled on in " + shortHome(openclawConfigPath()) + ", which was off, so its other hooks run too; uninstall turns it back off"
		}
		if _, err := setOpenClawHookEnabled(true); err != nil {
			return installResult{}, err
		}
	}
	return installResult{Path: dir, Action: a, Note: note}, nil
}

func openclawConfigPath() string {
	return filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
}

// openclawConfigAt is the value at a dotted path in openclaw.json, comments
// and all, or nil.
func openclawConfigAt(keys ...string) any {
	b, err := os.ReadFile(openclawConfigPath())
	if err != nil {
		return nil
	}
	var v any
	if json.Unmarshal([]byte(jsoncToJSON(string(b))), &v) != nil {
		return nil
	}
	for _, k := range keys {
		m, _ := v.(map[string]any)
		v = m[k]
	}
	return v
}

// openclawEntrySwitchedOff reports whether deja's entry under the block is
// there with enabled: false — what openclaw's own disable commands write.
func openclawEntrySwitchedOff(block, id string) bool {
	entry, _ := openclawConfigAt(append(strings.Split(block, "."), id)...).(map[string]any)
	return entry != nil && entry["enabled"] == false
}

// openclawHooksSwitchOff reports whether the reader has internal hooks off.
func openclawHooksSwitchOff() bool {
	keys := strings.Split(openclawHookEntries, ".")
	return openclawConfigAt(append(keys[:len(keys)-1:len(keys)-1], openclawHookSwitch)...) == false
}

// setOpenClawHookEnabled flips hooks.internal.enabled and our entry. Without
// both, the pack is discovered and listed as ready but never invoked.
func setOpenClawHookEnabled(on bool) (string, error) {
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	old, err := readConfig(path)
	if err != nil {
		return "", err
	}
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		if !on {
			return "unchanged", nil
		}
		root = map[string]any{}
	} else if configIsJSONC(old) {
		// A comment is not a broken file, and this writer shares openclaw.json
		// with the MCP one — so refusing here left a target that wrote half its
		// wiring, or could not take its own hook back out (#2811).
		return setOpenClawEntryJSONC(path, old, openclawHookEntries, openclawHookName, openclawHookSwitch, on, nil)
	} else if json.Unmarshal(old, &root) != nil {
		return "", openclawParseError(path, old)
	}
	hooks, _ := root["hooks"].(map[string]any)
	internal, _ := mapAt(hooks, "internal")
	entries, _ := mapAt(internal, "entries")
	if !on {
		if entries == nil {
			return "unchanged", nil
		}
		delete(entries, openclawHookName)
		// Leave the user's other hooks alone — and the switch, unless deja is
		// what turned it on. It records that at install time, the way it
		// records a block it created, so an uninstall gives back a setting the
		// reader had rather than deleting one deja only overwrote (#2830, the
		// rule the text writer got in #2811).
		//
		// The switch goes back whether or not other entries remain. Restoring
		// it only inside the empty case meant a reader with a hook of their own
		// and the switch off got it back on: deja gone, their hook running
		// where it had not been. The JSONC writer restored it either way, so
		// the two spellings of the same file disagreed (#3204).
		if was := hookSwitchWas(path); was == nil {
			delete(internal, "enabled")
		} else {
			internal["enabled"] = was
		}
		forgetHookSwitch(path)
		if len(entries) == 0 {
			delete(internal, "entries")
		}
		if len(internal) == 0 {
			delete(hooks, "internal")
		}
		if len(hooks) == 0 {
			delete(root, "hooks")
		}
	} else {
		if hooks == nil {
			hooks = map[string]any{}
			root["hooks"] = hooks
		}
		if internal == nil {
			internal = map[string]any{}
			hooks["internal"] = internal
		}
		if entries == nil {
			entries = map[string]any{}
			internal["entries"] = entries
		}
		noteHookSwitch(path, internal["enabled"])
		internal["enabled"] = true
		entries[openclawHookName] = map[string]any{"enabled": true}
	}
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return "", err
	}
	next = append(next, '\n')
	return writeIfChanged(path, old, next)
}

// hookSwitchKeys name what deja found under the switch, in the wiring state
// beside the blocks it records there. Three states rather than two: the reader
// had it on, the reader had it off, or there was nothing there and it is
// deja's to remove.
func hookSwitchKeys() (deja, on, off string) {
	keys := strings.Split(openclawHookEntries, ".")
	base := flagRecordKey(keys, openclawHookSwitch)
	// The bare key is the one the text writer uses for "deja put this here",
	// so a config that gains or loses a comment between install and uninstall
	// still reads the same record (#2830).
	return base, base + "=true", base + "=false"
}

// noteHookSwitch records what was under the switch before deja wrote to it.
func noteHookSwitch(path string, was any) {
	deja, on, off := hookSwitchKeys()
	// A false is the reader's whenever it is read: deja only writes true. One
	// set after the first install replaces what that install recorded, or the
	// uninstall turned their hooks back on (#4472).
	if v, ok := was.(bool); ok && !v {
		forgetHookSwitch(path)
		noteBlockAdded(path, off)
		return
	}
	// Once. A second install reads the switch deja itself set on the first, so
	// recording again would say the reader had it on and hand it back that way
	// — which is #2830 again, by way of an upgrade.
	for _, k := range []string{deja, on, off} {
		if blockWasAdded(path, k) {
			return
		}
	}
	switch v, ok := was.(bool); {
	case !ok:
		// Absent, or something that is not a boolean: deja overwrites it and
		// takes it away again, since it cannot know what a `null` or a `0`
		// there was meant to say.
		noteBlockAdded(path, deja)
	case v:
		noteBlockAdded(path, on)
	default:
		noteBlockAdded(path, off)
	}
}

// hookSwitchWas is what to put back, or nil where the switch is deja's own.
func hookSwitchWas(path string) any {
	_, on, off := hookSwitchKeys()
	switch {
	case blockWasAdded(path, on):
		return true
	case blockWasAdded(path, off):
		return false
	}
	return nil
}

func forgetHookSwitch(path string) {
	deja, on, off := hookSwitchKeys()
	forgetBlockAdded(path, deja)
	forgetBlockAdded(path, on)
	forgetBlockAdded(path, off)
}

const (
	// openclawHookEntries and openclawHookSwitch are where the bootstrap hook
	// lives and the switch that runs it, named once so the two writers agree.
	openclawHookEntries = "hooks.internal.entries"
	openclawHookSwitch  = "enabled"
)

// flagRecordKey names the switch in the wiring state, beside the blocks deja
// records there. A key of its own rather than the block's, so "deja created
// this block" and "deja turned this switch on" cannot be confused.
func flagRecordKey(keys []string, flagKey string) string {
	return strings.Join(keys[:len(keys)-1], ".") + "." + flagKey
}

// openclawParseError is the refusal for an openclaw.json deja cannot read.
// OpenClaw parses it with JSON5, so unquoted keys and single quotes are a valid
// config there; the strict parser's "invalid character 'a'", or "'/'" for the
// comment above them, sent the reader after the wrong thing (#4557). The
// error is the JSONC reading's, which gets past comments and trailing commas.
func openclawParseError(path string, old []byte) error {
	var v any
	err := json.Unmarshal([]byte(jsoncToJSON(string(old))), &v)
	if err == nil {
		// It reads, and is not an object: a list, a bare value.
		var root map[string]any
		err = json.Unmarshal([]byte(jsoncToJSON(string(old))), &root)
	}
	return configParseError(path, fmt.Errorf("%v — OpenClaw reads this file as JSON5, and deja edits it as JSON with comments: quote its keys and strings, or add deja by hand", err))
}

// setOpenClawEntryJSONC writes one of openclaw's entries — the bootstrap hook,
// or the plugin — into a config carrying comments, as text, so the reader's own
// lines stay where they are.
//
// flagKey is the switch beside the entries block, "" where there is none. For
// the hook it matters as much as the entry does: without it the pack is
// discovered, listed as ready, and never invoked, so the two are written
// together and taken back out together (#2811).
//
// want is the entry to write, nil for {"enabled": true}.
func setOpenClawEntryJSONC(path string, old []byte, blockKey, id, flagKey string, on bool, want map[string]any) (string, error) {
	text := lfText(old)
	var root map[string]any
	// Trailing commas too: configIsJSONC sends a file here for those alone,
	// and the uninstall refused what the install had just edited (#4557).
	if err := json.Unmarshal([]byte(jsoncToJSON(text)), &root); err != nil {
		return "", configParseError(path, err)
	}
	keys := strings.Split(blockKey, ".")
	// A key holding something other than an object is a config deja does not
	// understand, and the text writer would insert a second key of the same
	// name beside it — where the reader's value wins the decode and deja is
	// silently unwired (#2399, and #2811 for this writer).
	holder := root
	for i, key := range keys {
		v, present := holder[key]
		if !present {
			break
		}
		next, isObject := v.(map[string]any)
		if !isObject {
			if !on {
				return "unchanged", nil
			}
			return "", fmt.Errorf("%s: %q is not an object deja can edit — left as it was",
				path, strings.Join(keys[:i+1], "."))
		}
		holder = next
	}
	holders := chainHolders(root, keys)
	held := holders[len(keys)-1]
	have, _ := mapAt(held, keys[len(keys)-1])
	if !on {
		if have[id] == nil {
			return "unchanged", nil
		}
		delete(have, id)
		dropFrom := len(keys)
		// The switch comes out only where deja is what turned it on. A reader
		// who had set it themselves keeps it: deleting it left their own hook
		// wired and switched off (#2811).
		dropFlag := flagKey != "" && blockWasAdded(path, flagRecordKey(keys, flagKey))
		if len(have) == 0 && blockWasAdded(path, blockKey) {
			dropFrom = len(keys) - 1
			forgetBlockAdded(path, blockKey)
			// The switch goes with the entries it was for, and so does each
			// level above that deja created and that holds nothing else.
			if dropFlag {
				delete(held, flagKey)
			}
			for i := len(keys) - 2; i >= 0; i-- {
				prefix := strings.Join(keys[:i+1], ".")
				if len(holders[i+1]) != 1 || !blockWasAdded(path, prefix) {
					break
				}
				dropFrom = i
				forgetBlockAdded(path, prefix)
			}
		}
		next, err := jsoncSetEntry(text, blockKey, id, "", true, dropFrom)
		if err != nil {
			return "", configParseError(path, err)
		}
		// Only where the entries block deja created has emptied — the same
		// condition the parsed path uses. Taken on the ordinary
		// entry-comes-out case it deleted a switch the reader had set
		// themselves, leaving their own hook wired and turned off (#2811).
		if dropFlag {
			forgetBlockAdded(path, flagRecordKey(keys, flagKey))
		}
		// A switch deja turned on over the reader's own "off" goes back to off
		// rather than staying on: an uninstall that leaves internal hooks
		// running for someone who had switched them off is a setting deja
		// changed and did not give back.
		if !dropFlag && flagKey != "" && blockWasAdded(path, flagRecordKey(keys, flagKey)+"=false") {
			forgetBlockAdded(path, flagRecordKey(keys, flagKey)+"=false")
			next, err = jsoncSetFlag(next, strings.Join(keys[:len(keys)-1], "."), flagKey, false)
			if err != nil {
				return "", configParseError(path, err)
			}
		}
		if dropFlag && dropFrom >= len(keys)-1 {
			// The chain stayed, so the switch is still in it and comes out on
			// its own.
			flagBlock := strings.Join(keys[:len(keys)-1], ".")
			next, err = jsoncRemoveKey(next, flagBlock, flagKey, len(keys)-1)
			if err != nil {
				return "", configParseError(path, err)
			}
		}
		return writeIfChanged(path, old, []byte(next))
	}
	if have == nil {
		for i := range keys {
			if _, ok := holders[i][keys[i]].(map[string]any); !ok {
				noteBlockAdded(path, strings.Join(keys[:i+1], "."))
			}
		}
	}
	if want == nil {
		want = map[string]any{"enabled": true}
	}
	entry, err := jsoncEntryText(want)
	if err != nil {
		return "", err
	}
	next, err := jsoncSetEntry(text, blockKey, id, entry, false, len(keys))
	if err != nil {
		return "", configParseError(path, err)
	}
	if flagKey != "" {
		// Recorded the way a block deja created is, so an uninstall can tell a
		// switch deja turned on from one the reader set.
		switch was, present := held[flagKey]; {
		case !present:
			noteBlockAdded(path, flagRecordKey(keys, flagKey))
		case was == false:
			// Theirs, and off: deja needs it on while it is installed, so what
			// it changed is written down to be put back. The boolean only —
			// deja cannot know what a `null` or a `0` there was meant to say,
			// and writing a guess back is worse than leaving the switch on.
			noteBlockAdded(path, flagRecordKey(keys, flagKey)+"=false")
		}
		next, err = jsoncSetFlag(next, strings.Join(keys[:len(keys)-1], "."), flagKey, true)
		if err != nil {
			return "", configParseError(path, err)
		}
	}
	return writeIfChanged(path, old, []byte(next))
}

func mapAt(parent map[string]any, key string) (map[string]any, bool) {
	if parent == nil {
		return nil, false
	}
	m, ok := parent[key].(map[string]any)
	return m, ok
}

func openclawHookDoc() string {
	return `---
name: ` + openclawHookName + `
description: "Recall the user's past sessions from deja at agent bootstrap"
metadata:
  {
    "openclaw":
      {
        "emoji": "🧠",
        "events": ["agent:bootstrap"],
      },
  }
---

# deja recall

Generated by ` + "`deja install openclaw-auto`" + ` — safe to delete.

Adds one Project Context entry holding deja's digest of the user's prior
sessions, so a fresh OpenClaw session starts knowing what was already done.
`
}

func openclawHandlerJS(exe string) string {
	return fmt.Sprintf(`// generated by deja install — safe to delete; regenerate with: deja install openclaw-auto
import { execFileSync } from "node:child_process";

const DEJA = %q;

// The event fires on every agent run, and deja's plugin answers the same one
// in the same process. Both name the session by its transcript id and ask
// once, so whichever runs first carries the digest and the rest of the
// session's runs get nothing.
export default async (event) => {
  if (event?.type !== "agent" || event?.action !== "bootstrap") return;
  const context = event.context;
  if (!context || !Array.isArray(context.bootstrapFiles)) return;
  try {
    const digest = execFileSync(DEJA, ["hook-context", "--plain"], {
      input: JSON.stringify({
        session_id: context.sessionId || context.sessionKey || event.sessionKey || "",
        cwd: context.workspaceDir || process.cwd(),
        source: "startup",
        deja_once: true,
      }),
      encoding: "utf8",
      timeout: 10000,
      maxBuffer: 4 * 1024 * 1024,
      stdio: ["pipe", "pipe", "ignore"],
    }).trim();
    if (!digest) return;
    context.bootstrapFiles.push({
      name: "DEJA-RECALL.md",
      path: "deja://recall",
      content: digest,
      missing: false,
    });
  } catch {
    // memory is optional: never break the session over it
  }
};
`, exe)
}
