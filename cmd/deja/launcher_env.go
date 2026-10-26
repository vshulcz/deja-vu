package main

import (
	"path/filepath"
	"sort"
	"strings"
)

// Muse Code starts hooks with HOME, PATH and a few basics, nothing else, so a
// reader who moved deja's index, its settings or a store with XDG_* or DEJA_*
// got hooks reading another index and another policy than the CLI did, and
// Muse sessions under a custom XDG_DATA_HOME were never found by a hook
// (#4738). The launcher every hook runs through carries the values in effect at
// install, as fallbacks: a host that passes the environment through keeps its
// own, and one that strips it gets the same store the CLI uses.
//
// Only what decides where deja reads and writes and what it indexes. Never a
// key or a connection string, never a debug or test switch, never a harness's
// own home: CLAUDE_CONFIG_DIR and the like pick a profile per launch, and a
// value baked from one shell would follow every other profile's hooks. A store
// that falls out of a hook's view that way is kept by the index, not dropped
// (#4739).

// launcherEnvXDG are the base directories deja resolves its own files and the
// stores it reads under, with their defaults below the home directory. A value
// equal to the default, or relative (which the spec says to ignore), adds
// nothing.
var launcherEnvXDG = map[string]string{
	"XDG_CONFIG_HOME": ".config",
	"XDG_DATA_HOME":   filepath.Join(".local", "share"),
	"XDG_CACHE_HOME":  ".cache",
	"XDG_STATE_HOME":  filepath.Join(".local", "state"),
}

// launcherEnvSettings are the DEJA_* switches that change what is indexed or
// recalled, beside the location variables launcherEnvLocation matches.
var launcherEnvSettings = map[string]bool{
	"DEJA_INCLUDE_SUBAGENTS":     true,
	"DEJA_STORES":                true,
	"DEJA_EXCLUDE_PROJECTS":      true,
	"DEJA_EXCLUDE_HARNESSES":     true,
	"DEJA_INDEX_TOOL_OUTPUT":     true,
	"DEJA_INDEX_PATHS":           true,
	"DEJA_INDEX_WRITES":          true,
	"DEJA_INDEX_EDITS":           true,
	"DEJA_INDEX_COMMANDS":        true,
	"DEJA_RECALL":                true,
	"DEJA_RECALL_GLOBAL":         true,
	"DEJA_MACHINE":               true,
	"DEJA_AUTORECALL_LOCAL_ONLY": true,
	"DEJA_NO_AUTO_INDEX":         true,
	"DEJA_SEARCH_ONLY":           true,
}

// launcherEnvLocation matches the DEJA_* variables that name a path: the index
// directory, the policy and notes files, every store root and database.
func launcherEnvLocation(name string) bool {
	// DEJA_PASS_* are a test stand's markers, not settings.
	if !strings.HasPrefix(name, "DEJA_") || strings.HasPrefix(name, "DEJA_PASS_") {
		return false
	}
	for _, s := range []string{"_ROOT", "_ROOTS", "_DB", "_DIR", "_DIRS", "_FILE", "_HOME", "_CONFIG"} {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

// launcherEnv picks, from environ, the variables the launcher carries, sorted
// by name so the same environment writes the same file.
func launcherEnv(environ []string) [][2]string {
	home := homeDir()
	var out [][2]string
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || value == "" {
			continue
		}
		switch {
		case launcherEnvXDG[name] != "":
			if !filepath.IsAbs(value) || (home != "" && filepath.Clean(value) == filepath.Join(home, launcherEnvXDG[name])) {
				continue
			}
		case launcherEnvSettings[name], launcherEnvLocation(name):
		default:
			continue
		}
		out = append(out, [2]string{name, value})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}
