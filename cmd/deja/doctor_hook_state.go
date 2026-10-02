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
	want    int            // events this release wires for this client's version
	dead    bool           // the entries name a deja that is not there
	runNote string         // or name one the host's shell cannot reach as spelled (#4125)
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
	wiring := claudeWiring()
	st.want = len(wiring)
	for _, h := range wiring {
		if !hookEventWired(st.hooks, h.Event, h.Sub) {
			st.missing = append(st.missing, h.Event)
		}
	}
	switch {
	case len(st.missing) == st.want:
		st.state = "missing"
	case len(st.missing) > 0:
		st.state = "out of date"
	default:
		st.state = "wired"
	}
	// Only when something here is actually wired: both notes are about the
	// binary the entries name, and a file with no deja in it names none.
	if len(st.missing) < st.want {
		st.dead = hookExeNote(st.path, "claude-auto") != ""
	}
	if doctorLauncherNote(st.path, "claude-auto") != "" {
		st.dead = true
	}
	// A binary that is there can still be one the shell cannot reach as the
	// file spells it (#4125); asked only when nothing above already said dead.
	if !st.dead && len(st.missing) < st.want {
		st.runNote = claudeHookRunNote(st.hooks)
		st.dead = st.runNote != ""
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
		if json.Unmarshal(b, &root) != nil {
			// Nothing in it can be read as deja's, so neither can the trust
			// codex keeps for it.
			st.state = "unreadable"
			return st
		}
		st.hooks, _ = root["hooks"].(map[string]any)
		// Codex users keep their own hooks here, so the file being there
		// says nothing about deja: with none of deja's events in it, the
		// trust store below is about someone else's hook (#4297).
		if !codexHooksHoldDejas(st.hooks) {
			st.state, st.hooks = "missing", nil
			if codexPluginInstalled() {
				st.state = "plugin"
			}
			return st
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
	// shown is the state where it silently runs nothing. The pin read is the
	// one for deja's own entry — a user's hook ahead of it under the same
	// event holds the pin at :0:0 (#4313).
	pins := codexDejaPins(string(cfg), st.path, st.hooks)
	st.state = "untrusted"
	if section := codexPrimaryPin(pins); section != "" {
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
	st.approved, st.pinned = codexApprovedHooks(pins, codexHookWiring)
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
// pin for deja's own entry, and how many were looked for.
func codexApprovedHooks(pins map[string]string, wiring []struct{ Event, Sub, Matcher string }) (approved, pinned int) {
	for _, h := range wiring {
		pinned++
		if pins[h.Event] != "" {
			approved++
		}
	}
	return approved, pinned
}

// codexDejaHookPos is where deja's entry for an event sits in hooks.json: the
// group and the hook within it, the two numbers codex keys its trust by.
func codexDejaHookPos(hooks map[string]any, event, sub string) (codexHookPos, bool) {
	groups, _ := hooks[event].([]any)
	for g, groupAny := range groups {
		group, _ := groupAny.(map[string]any)
		list, _ := group["hooks"].([]any)
		for j, hAny := range list {
			h, _ := hAny.(map[string]any)
			if h == nil || h["type"] != "command" {
				continue
			}
			if isDejaHookCommand(h["command"], "deja "+sub) {
				return codexHookPos{codexEventKey(event), g, j}, true
			}
		}
	}
	return codexHookPos{}, false
}

// codexDejaPins maps each event deja has an entry for in the hooks file at
// path to the table codex's config keeps for that entry, "" when there is none.
// Only that file's keys count: a project's own hooks.json or a plugin's carries
// pins at the same positions for hooks that are not deja's.
//
// Codex keys its trust store per hook — `[hooks.state."<path>/hooks.json:
// <event>:<group>:<hook>"]` — so a user's own hook ahead of deja's under the
// same event holds the first key, and reading that one as deja's called a
// machine trusted where codex runs none of deja's hooks (#4313). A table ends
// at the next header; one inside a multi-line string is text.
//
// It deliberately does not check the recorded hash. deja cannot reproduce it:
// on codex 0.142.4 the pin for a hook whose command is one line long is not the
// sha256 of the hook file, of the command, of the handler object in any
// serialisation, or of any combination of the two with the matcher, the event
// or the key — checked. Presence of a pin is what deja can honestly read: it
// means codex has been shown this hook and kept an opinion about it.
func codexDejaPins(cfg, path string, hooks map[string]any) map[string]string {
	out := map[string]string{}
	spellings := codexHookPathSpellings(path)
	at := map[codexHookPos]string{}
	for _, h := range codexHookWiring {
		if pos, ok := codexDejaHookPos(hooks, h.Event, h.Sub); ok {
			at[pos], out[h.Event] = h.Event, ""
		}
	}
	lines := strings.SplitAfter(cfg, "\n")
	event, start := "", -1
	closeTable := func(end int) {
		if event != "" && out[event] == "" {
			out[event] = strings.Join(lines[start:end], "")
		}
		event, start = "", -1
	}
	inString := ""
	for i, line := range lines {
		opened := inString
		inString = multilineStringAfter(line, inString)
		if opened != "" || !strings.HasPrefix(strings.TrimSpace(line), "[") {
			continue
		}
		closeTable(i)
		key, _, _, _, ok := codexTrustKey(line)
		if !ok {
			continue
		}
		pos, file, ok := parseCodexTrustKey(key)
		if ok && spellings[file] {
			if e, ours := at[pos]; ours {
				event, start = e, i
			}
		}
	}
	closeTable(len(lines))
	return out
}

// codexPrimaryPin is the table for the first event deja has an entry for —
// SessionStart on any install — which decides the row's trust state.
func codexPrimaryPin(pins map[string]string) string {
	for _, h := range codexHookWiring {
		if section, ok := pins[h.Event]; ok {
			return section
		}
	}
	return ""
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

// codexHooksHoldDejas reports whether any event deja wires for codex is in
// hooks.json.
func codexHooksHoldDejas(hooks map[string]any) bool {
	for _, h := range codexHookWiring {
		if hookEventWired(hooks, h.Event, h.Sub) {
			return true
		}
	}
	return false
}
