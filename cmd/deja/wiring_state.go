package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// A hook or plugin deja wrote is a copy of what this binary generates, frozen
// at install time. Upgrade the binary and those copies stay as they were:
// when a generator changes — a wrong event name, a timeout in the wrong unit —
// the fix ships and never reaches anyone, because nobody re-runs an installer
// that already succeeded. That is how qwen and kimi sat with dead wiring here
// for weeks.
//
// So deja records what it wired and with which version, and refreshes those
// same targets after an upgrade. Only targets it installed itself, never a
// new one: this repairs, it does not spread.
type wiringState struct {
	Version string   `json:"version"`
	Targets []string `json:"targets"`
	// Home is the home directory the targets were written under. The repair
	// derives every path from the environment it runs in, so a process whose
	// HOME points elsewhere while the record is still visible — sudo with a
	// preserved XDG_CONFIG_HOME, a container, an su session — wrote a fresh
	// set of configs into a home nobody installed into, left the real ones
	// pointing at the old binary, and marked the record repaired (#885).
	Home string `json:"home,omitempty"`
	// Exe is the binary path the configs were written with. A move without a
	// version change is ordinary — a relink, a reinstall of the same release,
	// a `go install` over a manual download — and left every config pointing
	// at a path that no longer exists (#773).
	Exe string `json:"exe,omitempty"`
}

func wiringStatePath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(homeDir(), ".config")
	}
	return filepath.Join(base, "deja", "wiring.json")
}

func readWiringState() wiringState {
	var st wiringState
	b, err := os.ReadFile(wiringStatePath())
	if err != nil {
		return st
	}
	_ = json.Unmarshal(b, &st)
	return st
}

// recordWiring remembers the targets an install wrote. Uninstalled ones drop
// out, so a user who removes a harness is not re-wired behind their back.
func recordWiring(targets []string, uninstall bool) {
	st := readWiringState()
	have := map[string]bool{}
	for _, t := range st.Targets {
		have[t] = true
	}
	for _, t := range targets {
		if t == "statusline" {
			continue
		}
		have[t] = !uninstall
	}
	var kept []string
	for t, on := range have {
		if on {
			kept = append(kept, t)
		}
	}
	sort.Strings(kept)
	exe, _ := os.Executable()
	exe, _ = filepath.Abs(exe)
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	// Uninstalling everything on a machine that was never wired would otherwise
	// create the record on the way out — a file left behind by the command that
	// removes things (#676).
	if len(kept) == 0 {
		if _, err := os.Stat(wiringStatePath()); os.IsNotExist(err) {
			return
		}
	}
	st = wiringState{Version: version, Targets: kept, Exe: exe, Home: homeDir()}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	path := wiringStatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, append(b, '\n'), 0o644)
}

// refreshWiringAfterUpgrade rewrites the recorded targets when the binary that
// wrote them is not the one running now. It returns the targets it changed, so
// a caller with somewhere to print can say so; the hook paths call it and stay
// quiet, because a session start is not the place for maintenance chatter.
func refreshWiringAfterUpgrade() []string {
	st := readWiringState()
	if len(st.Targets) == 0 || version == "" {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	exe, _ = filepath.Abs(exe)
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	// Either the version or the path: a binary that moved writes the same
	// version into configs that now name a file which is not there (#773).
	if st.Version == version && (st.Exe == "" || st.Exe == exe) {
		return nil
	}
	// Only the home the targets were written under: this repairs, it does not
	// spread (#885).
	if st.Home != "" && st.Home != homeDir() {
		return nil
	}
	var changed []string
	for _, target := range st.Targets {
		res, err := installTarget(target, exe, false)
		if err != nil {
			// A harness the user has since removed is not an error worth
			// surfacing: the next install run will drop it from the record.
			continue
		}
		if res.Action != "" && res.Action != "unchanged" {
			changed = append(changed, target)
		}
	}
	recordWiring(st.Targets, false)
	return changed
}
