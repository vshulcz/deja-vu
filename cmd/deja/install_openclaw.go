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
//   - The event only fires in gateway mode, and only when the agent workspace
//     is first bootstrapped — not once per session. `openclaw agent --local`
//     never emits it at all, so the pack looks dead when tested that way.
//   - Internal hooks are off wholesale until hooks.internal.enabled is set, and
//     a pack that is listed as "ready" still never runs until then.
//
// OpenClaw's default backend is claude-cli, which already inherits deja's
// Claude Code hook; this pack is what covers its other providers.
const openclawHookName = "deja-recall"

func installOpenClawHooks(exe string, uninstall bool) (installResult, error) {
	dir := filepath.Join(sources.OpenClawStateDir(), "hooks", openclawHookName)
	if uninstall {
		if err := os.RemoveAll(dir); err != nil {
			return installResult{}, err
		}
		if _, err := setOpenClawHookEnabled(false); err != nil {
			return installResult{}, err
		}
		return installResult{Path: dir, Action: "removed"}, nil
	}
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
	if _, err := setOpenClawHookEnabled(true); err != nil {
		return installResult{}, err
	}
	return installResult{Path: dir, Action: a}, nil
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
		return setOpenClawEntryJSONC(path, old, "hooks.internal.entries", openclawHookName, "enabled", on)
	} else if err := json.Unmarshal(old, &root); err != nil {
		return "", configParseError(path, err)
	}
	hooks, _ := root["hooks"].(map[string]any)
	internal, _ := mapAt(hooks, "internal")
	entries, _ := mapAt(internal, "entries")
	if !on {
		if entries == nil {
			return "unchanged", nil
		}
		delete(entries, openclawHookName)
		// Leave the user's other hooks — and the master switch — alone.
		if len(entries) == 0 {
			delete(internal, "entries")
			delete(internal, "enabled")
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

// flagRecordKey names the switch in the wiring state, beside the blocks deja
// records there. A key of its own rather than the block's, so "deja created
// this block" and "deja turned this switch on" cannot be confused.
func flagRecordKey(keys []string, flagKey string) string {
	return strings.Join(keys[:len(keys)-1], ".") + "." + flagKey
}

// setOpenClawEntryJSONC writes one of openclaw's entries — the bootstrap hook,
// or the plugin — into a config carrying comments, as text, so the reader's own
// lines stay where they are.
//
// flagKey is the switch beside the entries block, "" where there is none. For
// the hook it matters as much as the entry does: without it the pack is
// discovered, listed as ready, and never invoked, so the two are written
// together and taken back out together (#2811).
func setOpenClawEntryJSONC(path string, old []byte, blockKey, id, flagKey string, on bool) (string, error) {
	text := string(old)
	var root map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(text)), &root); err != nil {
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
	entry, err := jsoncEntryText(map[string]any{"enabled": true})
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
		if _, present := held[flagKey]; !present {
			noteBlockAdded(path, flagRecordKey(keys, flagKey))
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

export default async (event) => {
  if (event?.type !== "agent" || event?.action !== "bootstrap") return;
  const context = event.context;
  if (!context || !Array.isArray(context.bootstrapFiles)) return;
  try {
    const digest = execFileSync(DEJA, ["hook-context", "--plain"], {
      encoding: "utf8",
      timeout: 10000,
      maxBuffer: 4 * 1024 * 1024,
      stdio: ["ignore", "pipe", "ignore"],
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
