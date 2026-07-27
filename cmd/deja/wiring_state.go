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
	st = wiringState{Version: version, Targets: kept}
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
	if len(st.Targets) == 0 || st.Version == version || version == "" {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	exe, _ = filepath.Abs(exe)
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
