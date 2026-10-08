package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// OpenClaw 2026.8.1 put agent_turn_prepare and before_prompt_build behind a
// grant: a plugin that is not bundled gets them only with
// plugins.entries.<id>.hooks.allowConversationAccess set to true, and without
// it the gateway logs `typed hook "before_prompt_build" blocked` and drops the
// handler (registerTypedHook in src/plugins/loader, 2026.9.8). That is the
// per-prompt recall, and with it the packet after a compaction. The digest
// rides the plugin's agent:bootstrap hook, which is not behind it, and neither
// are the tool middleware, before_compaction and session_end.
//
// The key itself is older than the gate: 2026.4.24 added it to the plugin
// entry schema, which is strict, and 2026.4.23 refuses to start on a config
// that has it ("Unrecognized key"). So install writes it only where the
// OpenClaw on this machine knows it.
const (
	openclawAccessKey   = "allowConversationAccess"
	openclawAccessSince = "2026.4.24"
	openclawAccessGate  = "2026.8.1"
)

// openclawPluginEntry is deja's entry under plugins.entries, built on the one
// that is there: the reader's other keys stay, enabled: false stays (#4472),
// and so does an allowConversationAccess the reader set to false.
func openclawPluginEntry(have map[string]any, grant bool) (map[string]any, string) {
	entry := map[string]any{}
	for k, v := range have {
		entry[k] = v
	}
	var note string
	if entry["enabled"] == false {
		note = "left deja's plugin switched off, the way it was — `openclaw plugins enable deja` turns it back on"
	} else {
		entry["enabled"] = true
	}
	hooks := map[string]any{}
	if h, ok := entry["hooks"].(map[string]any); ok {
		for k, v := range h {
			hooks[k] = v
		}
	} else if entry["hooks"] != nil {
		// Not an object: a config deja does not understand, left as it is.
		return entry, note
	}
	switch {
	case !grant:
		// An OpenClaw that predates the key rejects the whole config over it.
		delete(hooks, openclawAccessKey)
	case hooks[openclawAccessKey] == false:
		if note == "" {
			note = "left plugins.entries.deja.hooks." + openclawAccessKey + " false, the way it was — OpenClaw " + openclawAccessGate + "+ then blocks deja's per-prompt recall"
		}
	default:
		hooks[openclawAccessKey] = true
	}
	if len(hooks) == 0 {
		delete(entry, "hooks")
	} else {
		entry["hooks"] = hooks
	}
	return entry, note
}

// openclawTakesConversationAccess reports whether the OpenClaw here accepts
// the key. Unknown counts as yes: every build since 2026.4.24 does, and the
// ones that block deja without it are the current ones.
func openclawTakesConversationAccess() bool {
	v := openclawVersion()
	return v == "" || openclawVersionAtLeast(v, openclawAccessSince)
}

// openclawVersion is the version of the OpenClaw on PATH, read from its
// package.json, or else the one that last wrote openclaw.json; "" when
// neither says.
func openclawVersion() string {
	if v := openclawPackageVersion(); v != "" {
		return v
	}
	v, _ := openclawConfigAt("meta", "lastTouchedVersion").(string)
	return strings.TrimSpace(v)
}

// openclawPackageVersion follows the openclaw command to the package it
// starts: npm links bin/openclaw to lib/node_modules/openclaw/openclaw.mjs,
// and on Windows leaves openclaw.cmd beside node_modules/openclaw.
func openclawPackageVersion() string {
	bin, err := exec.LookPath("openclaw")
	if err != nil {
		return ""
	}
	var dirs []string
	if real, err := filepath.EvalSymlinks(bin); err == nil {
		for d := filepath.Dir(real); len(dirs) < 4; d = filepath.Dir(d) {
			dirs = append(dirs, d)
			if filepath.Dir(d) == d {
				break
			}
		}
	}
	dirs = append(dirs, filepath.Join(filepath.Dir(bin), "node_modules", "openclaw"))
	for _, d := range dirs {
		b, err := os.ReadFile(filepath.Join(d, "package.json"))
		if err != nil {
			continue
		}
		var pkg struct{ Name, Version string }
		if json.Unmarshal(b, &pkg) == nil && pkg.Name == "openclaw" {
			return pkg.Version
		}
	}
	return ""
}

// openclawVersionAtLeast compares OpenClaw's date versions, 2026.9.8 or
// 2026.7.1-2, by their first three numbers. One it cannot read counts as
// new.
func openclawVersionAtLeast(v, min string) bool {
	a, okA := openclawVersionParts(v)
	b, okB := openclawVersionParts(min)
	if !okA || !okB {
		return true
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return true
}

func openclawVersionParts(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) < 3 {
		return out, false
	}
	for i := range out {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
