package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// hookWiringState is one of the two hook wirings that predate autoWirings and
// print their own doctor lines. Both surfaces read it, so the text report and
// the JSON cannot disagree about whether the hook is live — which is the whole
// point of the table the other harnesses share.
type hookWiringState struct {
	name    string
	path    string
	state   string
	missing []string       // events this release wires that the file lacks
	dead    bool           // the entries name a deja that is not there
	hooks   map[string]any // for the repeat check, which reads the entries
	absent  bool           // the file itself is not there, so there is nothing else to say
	// trustUnknown is codex's own state: hooks.json is wired and its trust
	// store cannot be read, so whether codex will run the hook is unknown.
	trustUnknown bool
}

// claudeHookWiringState reads ~/.claude/settings.json and decides the row.
func claudeHookWiringState() hookWiringState {
	st := hookWiringState{name: "claude-code", path: filepath.Join(sources.ClaudeConfigDir(), "settings.json")}
	b, err := os.ReadFile(st.path)
	if err != nil {
		st.state, st.absent = "missing", true
		return st
	}
	var root map[string]any
	if json.Unmarshal(b, &root) != nil {
		st.state = "unreadable"
		return st
	}
	st.hooks, _ = root["hooks"].(map[string]any)
	for _, h := range claudeHookWiring {
		if !hookEventWired(st.hooks, h.Event, h.Sub) {
			st.missing = append(st.missing, h.Event)
		}
	}
	switch {
	case len(st.missing) == len(claudeHookWiring):
		st.state = "missing"
	case len(st.missing) > 0:
		st.state = "out of date"
	default:
		st.state = "wired"
	}
	// Only when something here is actually wired: both notes are about the
	// binary the entries name, and a file with no deja in it names none.
	if len(st.missing) < len(claudeHookWiring) {
		st.dead = hookExeNote(st.path, "claude-auto") != ""
	}
	if doctorLauncherNote(st.path, "claude-auto") != "" {
		st.dead = true
	}
	return st
}

// codexHookWiringState reads codex's hooks.json and the trust store that
// decides whether codex runs it. Trusted is not the same as complete, and
// neither is the same as the binary being there.
func codexHookWiringState() hookWiringState {
	st := hookWiringState{name: "codex-hook", path: filepath.Join(sources.CodexHome(), "hooks.json")}
	if _, err := os.Stat(st.path); err != nil {
		st.state, st.absent = "missing", true
		// The plugin ships the same hooks under its own root, and codex trusts
		// those the same way. Nothing was installed here, and nothing is
		// missing either.
		if codexPluginInstalled() {
			st.state = "plugin"
		}
		return st
	}
	if b, err := os.ReadFile(st.path); err == nil {
		var root map[string]any
		if json.Unmarshal(b, &root) == nil {
			st.hooks, _ = root["hooks"].(map[string]any)
		}
	}
	// Whatever codex thinks of the entry, it can still name a binary that is
	// gone — and an untrusted row said only that codex had not been shown it,
	// which is the state an upgraded machine sits in (#3502).
	st.dead = hookExeNote(st.path, "codex-auto") != ""
	cfgPath := filepath.Join(sources.CodexHome(), "config.toml")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		// Codex's trust store is its config. Without it there is nothing to
		// read, and guessing either way is worse than saying so.
		st.state, st.trustUnknown = "wired", true
		return st
	}
	// Untrusted until the config says otherwise: an entry codex has never been
	// shown is the state where it silently runs nothing.
	st.state = "untrusted"
	if section := codexHookTrustSection(string(cfg)); section != "" {
		off, on := strings.Index(section, "enabled = false"), strings.Index(section, "enabled = true")
		switch {
		case off >= 0 && (on == -1 || on > off):
			st.state = "disabled"
		case strings.Contains(section, "trusted_hash = \"sha256:"):
			st.state = "wired"
		}
	}
	if st.state != "wired" {
		return st
	}
	for _, h := range codexHookWiring {
		if !hookEventWired(st.hooks, h.Event, h.Sub) {
			st.missing = append(st.missing, h.Event)
		}
	}
	if len(st.missing) > 0 {
		st.state = "out of date"
	}
	return st
}
