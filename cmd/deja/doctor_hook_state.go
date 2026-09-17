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
	// approved counts the hooks codex has a trust pin for, of the pinned it
	// was asked about. Both are zero when the config could not be read.
	approved int
	pinned   int
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
	// Trust is per hook, not per file. A machine that approved deja's hook
	// when there was one and has five now sits with four unapproved, and codex
	// runs none of those — its own screen says so ("5 hooks are new or
	// changed… Continue without trusting (hooks won't run)") while this row
	// said `wired` because the session_start pin was there (#3654).
	st.approved, st.pinned = codexApprovedHooks(string(cfg), codexHookWiring)
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

// codexApprovedHooks counts how many of the events deja wrote carry a trust
// pin in codex's config, and how many were looked for.
//
// The keys are `[hooks.state."<path>/hooks.json:<event>:<i>:<j>"]` with the
// event in snake_case — `session_start`, `user_prompt_submit`, `pre_tool_use`,
// `post_tool_use`, `pre_compact` — so the count is a read of the same store the
// single-hook check already parses.
func codexApprovedHooks(cfg string, wiring []struct{ Event, Sub, Matcher string }) (approved, pinned int) {
	for _, h := range wiring {
		pinned++
		if strings.Contains(cfg, "hooks.json:"+codexEventKey(h.Event)+":") {
			approved++
		}
	}
	return approved, pinned
}

// codexEventKey is the event name as codex writes it into a trust key:
// SessionStart becomes session_start.
func codexEventKey(event string) string {
	var out []rune
	for i, r := range event {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				out = append(out, '_')
			}
			r = r - 'A' + 'a'
		}
		out = append(out, r)
	}
	return string(out)
}
