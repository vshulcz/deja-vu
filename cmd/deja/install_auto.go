package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Codex (v0.114+) ships lifecycle hooks with the same JSON contract as
// Claude Code, so the existing `deja hook-context` output works unchanged.
// We merge one SessionStart entry into ~/.codex/hooks.json and leave
// everything else in the file alone.
// codexHookWiring is every event deja installs into Codex, in one place because
// install writes from it and doctor reads it. A hooks.json written by an older
// deja — SessionStart alone, where there are now four — was reported as wired
// on the strength of the trust entry, and the events added since reached
// nobody.
// hookWire is one event deja installs: the hook subcommand and its matcher.
type hookWire = struct{ Event, Sub, Matcher string }

var codexHookWiring = []hookWire{
	// SessionStart carries the project digest. No matcher, as in Claude: codex
	// fires this again with source "compact" after each compaction, and that is
	// the moment the digest is worth most — measured on codex 0.149.0, one
	// `codex exec` run compacted six times and "startup|resume" caught none of
	// them, so the agent came out of every compaction with nothing.
	{"SessionStart", "hook-context", ""},
	// UserPromptSubmit answers the prompt itself. Codex sends the same payload
	// Claude does — prompt, session_id, cwd — so hook-prompt reads it unchanged;
	// measured on codex 0.149.0, the event fires on every `codex exec` turn.
	{"UserPromptSubmit", "hook-prompt", ""},
	// PreToolUse carries the prior decision for the file or command about to
	// change, scoped to the tools that run a command or change a file (codex
	// edits via apply_patch) so the hook does not spawn on every read.
	{"PreToolUse", "hook-tool", "Bash|apply_patch"},
	// And the fix pair when a command fails, which is the moment an agent never
	// thinks to ask for it.
	{"PostToolUse", "hook-tool-after", "Bash"},
	// Compaction throws away the blocks this session was shown while the list
	// that stops them repeating outlives them, so without this the memory codex
	// just lost is the memory recall refuses to send again. Codex fires it with
	// trigger "auto", the same shape Claude sends.
	{"PreCompact", "hook-precompact", "manual|auto"},
	// Codex fires SessionEnd on exit (measured on 0.149.0). Without it the
	// session just quit kept its live stamp, and the next session's MCP recall
	// left it out for the rest of the window (#4545, #4210).
	{"SessionEnd", "hook-session-end", ""},
}

// codexHookTimeout is the timeout deja writes for a codex hook, in seconds.
// codex-cli 0.149.0 caps SessionEnd at 3 s and warns on every start when the
// file asks for more (#5126); the hook only drops the live stamp, so 3 s is
// plenty. Every other event keeps 10.
func codexHookTimeout(event string) int {
	if event == "SessionEnd" {
		return codexSessionEndCap
	}
	return 10
}

const codexSessionEndCap = 3

func installCodexHooks(exe string, uninstall bool) (installResult, error) {
	// Use CodexHome() (honours CODEX_HOME / DEJA_CODEX_ROOT) rather than a raw
	// ~/.codex join, so a sandboxed install stays sandboxed and a non-default
	// codex home gets its hooks written where codex actually reads them. Every
	// other codex path already goes through it (e.g. doctor.go). See #850.
	return installCodexHooksAt(exe, filepath.Join(sources.CodexHome(), "hooks.json"), filepath.Join(sources.CodexHome(), "config.toml"), codexHookWiring, uninstall)
}

// installCodexHooksAt writes wiring into the hooks file at path and moves the
// trust pins kept in cfgPath with it. TRAE CLI, a codex-rs fork, reads the
// same file in its own home.
func installCodexHooksAt(exe, path, cfgPath string, wiring []hookWire, uninstall bool) (installResult, error) {
	exe = hookExeFor(exe, uninstall)
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if uerr := json.Unmarshal(old, &root); uerr != nil {
		return installResult{}, configParseError(path, uerr)
	}
	// What the file held before, for the trust pins below: a second parse
	// rather than a copy, since the update edits the maps in place.
	var before map[string]any
	_ = json.Unmarshal(old, &before)
	for _, h := range wiring {
		updateCodexHook(root, h.Event, hookRun(exe, h.Sub), h.Matcher, uninstall)
	}
	if hooks, _ := root["hooks"].(map[string]any); len(hooks) == 0 {
		delete(root, "hooks")
	}
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	if err == nil && a != "unchanged" {
		// Codex's approval of each hook is pinned by position in its
		// config.toml; see codex_hook_trust.go. Install moves hooks too: a
		// second copy of deja's entry it drops shifts the reader's hooks
		// after it up a place (#4227).
		err = moveCodexHookTrust(cfgPath, path, before, root)
	}
	return installResult{Path: path, Action: a}, err
}

// updateCodexHook merges one deja hook for an event into codex's hooks.json,
// idempotently: it adopts an entry we already own (so a move or an upgrade
// updates in place) and adds one otherwise. Same shape as updateClaudeHook.
func updateCodexHook(root map[string]any, event, cmd, matcher string, uninstall bool) {
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	entries, _ := hooks[event].([]any)
	var kept []any
	found := false
	for _, entryAny := range entries {
		entry, _ := entryAny.(map[string]any)
		if entry != nil && entryHasCommand(entry, cmd) {
			if uninstall {
				continue
			}
			// One of ours is enough. A config can carry the entry twice — a
			// hand-edited copy, a merge that kept both sides — and adopting
			// each of them left the hook firing twice on every prompt.
			if found {
				continue
			}
			found = true
			adoptCodexHookEntry(entry, cmd, event)
			// An install from before #5126 wrote 10 here, which codex clamps
			// with a warning on every start. A lower value the reader set
			// stays.
			if event == "SessionEnd" {
				capHookTimeout(entry, cmd, codexSessionEndCap)
			}
			// The matcher is part of the wiring, not a user setting, so an
			// upgrade has to rewrite it: the SessionStart entry written when
			// this meant "startup|resume" went on missing every compaction
			// through any number of installs, because adopting it left the
			// pattern alone.
			if matcher == "" {
				delete(entry, "matcher")
			} else {
				entry["matcher"] = matcher
			}
		}
		kept = append(kept, entryAny)
	}
	if !uninstall && !found {
		h := map[string]any{"type": "command", "command": cmd, "timeout": codexHookTimeout(event)}
		// Codex surfaces statusMessage in its hook run summary.
		if msg := hookStatusMessage(event); msg != "" {
			h["statusMessage"] = msg
		}
		entry := map[string]any{"hooks": []any{h}}
		// An event that applies to every turn has no matcher, and codex's own
		// examples leave the key out rather than carrying an empty pattern.
		if matcher != "" {
			entry["matcher"] = matcher
		}
		kept = append(kept, entry)
	}
	if len(kept) == 0 {
		delete(hooks, event)
	} else {
		hooks[event] = kept
	}
}

// adoptHookTimeout sets the timeout on the hook deja owns inside an entry,
// leaving any line a reader wrote around it alone.
func adoptHookTimeout(entry map[string]any, cmd string, timeout int) {
	hs, _ := entry["hooks"].([]any)
	for _, hAny := range hs {
		h, _ := hAny.(map[string]any)
		if h == nil || h["type"] != "command" || hookCommandKindOf(h["command"], cmd) != hookDejas {
			continue
		}
		h["timeout"] = timeout
	}
}

// capHookTimeout lowers the timeout on the hook deja owns inside an entry to
// limit when it asks for more, or says nothing at all.
func capHookTimeout(entry map[string]any, cmd string, limit int) {
	hs, _ := entry["hooks"].([]any)
	for _, hAny := range hs {
		h, _ := hAny.(map[string]any)
		if h == nil || h["type"] != "command" || hookCommandKindOf(h["command"], cmd) != hookDejas {
			continue
		}
		if t, ok := h["timeout"].(float64); !ok || t > float64(limit) {
			h["timeout"] = limit
		}
	}
}

// adoptCodexHookEntry rewrites the command and status message of an entry deja
// already owns.
func adoptCodexHookEntry(entry map[string]any, cmd, event string) {
	hs, _ := entry["hooks"].([]any)
	for _, hAny := range hs {
		h, _ := hAny.(map[string]any)
		// hookDejas, not "deja runs in here somewhere": a line the reader
		// wrote around our hook is theirs, and rewriting it throws away the
		// rest of what it does (#2477).
		if h == nil || h["type"] != "command" || hookCommandKindOf(h["command"], cmd) != hookDejas {
			continue
		}
		h["command"] = cmd
		if msg := hookStatusMessage(event); msg != "" {
			h["statusMessage"] = msg
		}
	}
}

// entryHasCommand matches on the trailing subcommand rather than the whole
// string, so an install from a new binary path replaces our old entry instead
// of leaving it to fire alongside the new one.
func entryHasCommand(entry map[string]any, cmd string) bool {
	hs, _ := entry["hooks"].([]any)
	for _, hAny := range hs {
		h, _ := hAny.(map[string]any)
		if h != nil && h["type"] == "command" && isDejaHookCommand(h["command"], cmd) {
			return true
		}
	}
	return false
}

// Gemini CLI and Qwen Code both run a command before the agent loop, which is
// the same injection point Claude Code's SessionStart gives us — so
// auto-recall works there too, not just MCP on demand.
//
// They are not the same shape, and both differences were found by running
// them rather than by reading their bundles:
//
//   - Gemini calls the event BeforeAgent (it has no SessionStart) and reads
//     `timeout` in MILLISECONDS. A Claude-style `"timeout": 10` is ten
//     milliseconds there, and the hook is killed before it can answer.
//   - Qwen forked from an older Gemini and kept SessionStart with a matcher,
//     with `timeout` in seconds.
func installGeminiAuto(exe string, uninstall bool) (installResult, error) {
	// Clear the settings.json hook older versions wrote: it never fired, and
	// leaving it behind makes a dead integration look installed.
	if _, err := installSettingsHook(
		filepath.Join(sources.GeminiHome(), "settings.json"),
		"BeforeAgent", "", 10000, exe, true); err != nil {
		return installResult{}, err
	}
	return installGeminiExtension(exe, uninstall)
}

// Qwen's `timeout` is MILLISECONDS, like Gemini's — the 10 deja used to write
// killed the hook ten milliseconds in, which is indistinguishable from a
// harness that has no hooks.
//
// SessionStart used to be dropped here: qwen ran it and consumed nothing, so
// deja retired the entry rather than leave a hook answering into the void. That
// is no longer true — on qwen-code 0.20.0 the digest reaches the model, and so
// does what a hook returns after a tool. PreToolUse fires and its output does
// not, so it stays unwired.
//
// The failure is its own event. Measured on 0.20.0 against a recording
// endpoint: PostToolUse fires only when the tool succeeded — qwen guards it
// with `!toolResult.error` and calls firePostToolUseFailureHook otherwise — so
// the fix pair sat on the one event that never fires at a failure, which is the
// moment it exists for.
var qwenHookWiring = []struct{ Event, Sub, Matcher string }{
	{"SessionStart", "hook-context", ""},
	{"UserPromptSubmit", "hook-prompt", ""},
	// The fix pair, at the failure. Matched on the tool that runs a command so
	// it never spawns on a read.
	{"PostToolUseFailure", "hook-tool-after", "run_shell_command"},
	// The file line, after a read or an edit: PreToolUse is not read, and
	// what PostToolUse returns goes in beside the tool's result.
	{"PostToolUse", "hook-tool", "read_file|edit|write_file"},
	// Compaction throws away the blocks this session was shown while the list
	// that stops them repeating outlives them, so without this the memory qwen
	// just lost is the memory recall refuses to send again.
	{"PreCompact", "hook-precompact", ""},
	// The session is over, so its live stamp goes and the next session's MCP
	// recall can answer with it (#4257). qwen-code 0.20.0 fires it on an
	// interactive exit and from ACP; a one-shot `qwen -p` does not, so those
	// still wait out the window.
	{"SessionEnd", "hook-session-end", ""},
}

// qwenRetiredEvents are hooks deja used to write for qwen and no longer does,
// by event and subcommand. The PostToolUse fix pair fired only after a tool
// that succeeded, so it looked up a repair for commands that did not need one
// and stayed quiet for the ones that did; leaving it behind would keep that
// cost on every green command.
var qwenRetiredEvents = map[string]string{"PostToolUse": "hook-tool-after"}

func installQwenAuto(exe string, uninstall bool) (installResult, error) {
	exe = hookExeFor(exe, uninstall)
	path := filepath.Join(sources.QwenConfigDir(), "settings.json")
	var res installResult
	for i, h := range qwenHookWiring {
		r, err := installSettingsHookRetiring(path, h.Event, h.Matcher, 60000, hookRun(exe, h.Sub), uninstall, qwenRetiredEvents)
		if err != nil {
			return installResult{}, err
		}
		// What the target reports is the first change made, the way the other
		// multi-write targets do: "unchanged" from a later event must not
		// overwrite the first one's "updated".
		if i == 0 || (res.Action == "unchanged" && r.Action != "unchanged") {
			res = r
		}
	}
	return res, nil
}

// installSettingsHook merges one hook entry into a settings.json that the
// host also uses for everything else, leaving the rest of the file alone.
func installSettingsHook(path, event, matcher string, timeout int, exe string, uninstall bool) (installResult, error) { //nolint:unparam // the matcher is part of the entry it writes
	return installSettingsHookCmd(path, event, matcher, timeout, hookRun(exe, "hook-context"), uninstall)
}

func installSettingsHookCmd(path, event, matcher string, timeout int, cmd string, uninstall bool) (installResult, error) {
	return installSettingsHookRetiring(path, event, matcher, timeout, cmd, uninstall, nil)
}

// installSettingsHookRetiring also drops deja hooks this harness no longer
// uses: under each event in retire, the entries running that subcommand, or
// every deja entry when it is "". Without it a generator fix ships and the
// old, dead entry keeps firing next to the new one for everyone who installed
// before.
func installSettingsHookRetiring(path, event, matcher string, timeout int, cmd string, uninstall bool, retire map[string]string) (installResult, error) {
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var root map[string]any
	// A settings file carrying comments cannot be rewritten without losing
	// them, and a hook entry is an element in an event array rather than a key
	// in an object, so the text path the MCP writers take does not reach here
	// (#2744). What it can do is answer honestly: read the file with its
	// comments blanked, and when there is nothing of deja's to change, say
	// unchanged rather than refuse a file it was never going to touch.
	jsonc := configIsJSONC(old)
	source := old
	if jsonc {
		source = []byte(jsoncToJSON(string(old)))
	}
	if len(bytes.TrimSpace(source)) == 0 {
		root = map[string]any{}
	} else if uerr := json.Unmarshal(source, &root); uerr != nil {
		return installResult{}, configParseError(path, uerr)
	}
	before := ""
	if jsonc {
		b, merr := json.Marshal(root)
		if merr != nil {
			return installResult{}, merr
		}
		before = string(b)
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	for name, sub := range retire {
		if name == event && sub == "" {
			continue
		}
		old, _ := hooks[name].([]any)
		var survivors []any
		for _, entryAny := range old {
			entry, _ := entryAny.(map[string]any)
			if entry != nil && sub == "" && dejaHookEntry(entry) {
				continue
			}
			if entry != nil && sub != "" && entryHasCommand(entry, "deja "+sub) {
				continue
			}
			survivors = append(survivors, entryAny)
		}
		if len(survivors) == 0 {
			delete(hooks, name)
		} else {
			hooks[name] = survivors
		}
	}
	entries, _ := hooks[event].([]any)
	var kept []any
	found := false
	for _, entryAny := range entries {
		entry, _ := entryAny.(map[string]any)
		if entry != nil && entryHasCommand(entry, cmd) {
			if uninstall {
				continue
			}
			// Take the entry over rather than leaving it as it was: an install
			// from a new binary path, or from a version that gained a field,
			// has to update ours in place or the change never reaches anyone
			// who already had it. Only the first, for the same reason as
			// above: a doubled entry is a hook that runs twice.
			if found {
				continue
			}
			found = true
			adoptCodexHookEntry(entry, cmd, event)
			// The timeout too. Qwen and gemini read it in milliseconds, and the
			// 10 an older deja wrote kills the hook ten milliseconds in — which
			// looks exactly like a harness with no hooks. Adopting the entry
			// without this left everyone who installed before the fix with a
			// hook that could never answer.
			adoptHookTimeout(entry, cmd, timeout)
		}
		kept = append(kept, entryAny)
	}
	if !uninstall && !found {
		h := map[string]any{"type": "command", "command": cmd, "timeout": timeout}
		// The same line the adopt path above sets. Setting it only there meant
		// a first install wrote the entry without it and a second install
		// added it, so whoever installed once never saw the status line their
		// harness would have shown while the hook ran — and the two installs
		// produced different files, which is its own trap.
		if msg := hookStatusMessage(event); msg != "" {
			h["statusMessage"] = msg
		}
		entry := map[string]any{"hooks": []any{h}}
		if matcher != "" {
			entry["matcher"] = matcher
		}
		kept = append(kept, entry)
	}
	if len(kept) == 0 {
		delete(hooks, event)
	} else {
		hooks[event] = kept
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	}
	if jsonc {
		after, merr := json.Marshal(root)
		if merr != nil {
			return installResult{}, merr
		}
		if string(after) == before {
			// Nothing of deja's here either way: leave the reader's comments
			// where they are and report the truth.
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		return installResult{}, fmt.Errorf("%s: deja cannot edit hooks in a file that carries comments — add or remove the hook by hand, or take the comments out", path)
	}
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

// kimiHookMarker identifies our block in a config the user also edits by
// hand. Kimi keeps hooks in config.toml as an array of tables, so removal
// cannot key on a table name the way the MCP block does — several [[hooks]]
// entries are legal and only one of them is ours.
const kimiHookMarker = "# deja: auto-recall (managed by `deja install kimi-auto`)"

// Kimi Code's config is TOML, not JSON, and the entry is a flat table rather
// than the nested matcher/hooks shape Claude uses.
//
// Measured on 0.28.1, by reading the requests it sent: UserPromptSubmit is the
// only event whose output reaches the model, and it takes the hook's plain
// stdout — structured output carries permission decisions and nothing else. A
// SessionStart hook runs and its output goes nowhere; PreToolUse can block a
// tool but not add to it; PostToolUse and PostToolUseFailure are fire and
// forget. So the session digest rides the first prompt instead of a
// session-start channel, and hook-context --once is what keeps it to one.
//
// Several UserPromptSubmit hooks are allowed: kimi runs them all and joins
// their output, each in its own <hook_result> block.
func installKimiAuto(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.KimiConfigDir(), "config.toml")
	old, next, err := kimiHooksUpdate(path, hookExeFor(exe, uninstall), uninstall)
	if err != nil {
		return installResult{}, err
	}
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

// kimiHooksUpdate is installKimiAuto without the write.
func kimiHooksUpdate(path, exe string, uninstall bool) (old, next []byte, err error) {
	old, err = readConfig(path)
	if err != nil {
		return nil, nil, err
	}
	if !uninstall {
		if err := tomlInlineKey(lfText(old), "[[hooks]]"); err != nil {
			return nil, nil, configParseError(path, err)
		}
	}
	s := strings.TrimRight(removeKimiHookBlock(removeUnmarkedKimiDejaHooks(lfText(old), exe)), "\n")
	if !uninstall {
		var block []string
		for _, h := range kimiHooks {
			c := hookRun(exe, h.args...)
			if h.matcher == "" {
				block = append(block, kimiHookEntry(h.event, c))
			} else {
				block = append(block, kimiHookEntryMatching(h.event, h.matcher, c))
			}
		}
		if s != "" {
			s += "\n\n"
		}
		s += strings.Join(block, "\n")
	} else if s != "" {
		s += "\n"
	}
	return old, []byte(s), nil
}

// kimiHooks is what kimi-auto wires into config.toml.
//
// The digest and the per-prompt block both ride UserPromptSubmit.
// Compaction throws away what the session was shown, and the list that stops
// those blocks repeating has to go with it. Kimi waits for PreCompact before
// it compacts, so the session is read there from wire.jsonl and its packet
// rides the next prompt. The payload names only the session, hence --harness.
// SessionEnd: the session is over, so its live stamp goes, and the next
// session's MCP recall can answer with it now rather than in twenty minutes.
// Kimi Code 2.2 fires it only when a session is closed, and `kimi -p` exits
// without closing one, so a finished print run stayed hidden for the whole
// window (#5145). Stop ends every turn, print mode's last one included, so it
// clears the stamp too; the next turn's UserPromptSubmit stamps again. A turn
// that opens without a prompt — a background task's notice — or one a
// sub-agent's Stop cleared mid-way is stamped again by PreToolUse just before
// it calls deja's MCP tool, so recall still leaves the asking session out.
// A failed command's fix pair: Kimi fires PostToolUse(Failure) and drops what
// they print, so the pair waits for the session's next prompt
// (hook_deferred.go). The payload is snake_case with the shell tool named
// Bash and the failure under `error` (0.28.1 toHookInputData,
// notifyPostToolUse). The line about a file before Kimi edits it:
// runPreToolUse keeps only a block's reason, so the line waits for the next
// prompt too, with the edit made by then. The matcher is a regex tested
// unanchored, hence the anchors.
var kimiHooks = []struct {
	event, matcher string
	args           []string
}{
	{"UserPromptSubmit", "", []string{"hook-context", "--plain", "--once"}},
	{"UserPromptSubmit", "", []string{"hook-prompt", "--plain"}},
	{"PreCompact", "", []string{"hook-precompact", "--harness", "kimi"}},
	{"SessionEnd", "", []string{"hook-session-end"}},
	{"Stop", "", []string{"hook-session-end"}},
	{"PreToolUse", "^mcp__deja__", []string{"hook-mcp-call"}},
	{"PostToolUse", "Bash", []string{"hook-tool-after", "--defer"}},
	{"PostToolUseFailure", "Bash", []string{"hook-tool-after", "--defer"}},
	{"PreToolUse", "^(Edit|Write)$", []string{"hook-tool", "--defer"}},
}

// removeUnmarkedKimiDejaHooks drops a [[hooks]] table without deja's marker
// that is exactly one deja writes: the same event and matcher, deja's own hook
// line from any path, timeout 30, nothing else. Kimi Code's login and logout
// write config.toml back from its parsed form, which keeps the tables and
// drops every comment, the markers with them. Keyed on the marker alone,
// uninstall then left every hook running a deja-hook it had deleted, and
// install added a second set beside them. A rule the reader wrote around deja's
// hook differs in one of those and stays.
func removeUnmarkedKimiDejaHooks(s, exe string) string {
	lines := strings.Split(s, "\n")
	code := tomlCodeLines(s)
	var out []string
	for i := 0; i < len(lines); {
		if strings.TrimSpace(tomlCode(code[i])) != "[[hooks]]" || i > 0 && strings.TrimSpace(lines[i-1]) == kimiHookMarker {
			out = append(out, lines[i])
			i++
			continue
		}
		// The table ends at the next header or comment, as deja's marked block
		// does: a note the reader wrote above their next hook is theirs.
		end := i + 1
		for end < len(lines) {
			t := strings.TrimSpace(code[end])
			if strings.HasPrefix(t, "[") || strings.HasPrefix(t, "#") {
				break
			}
			end++
		}
		if !kimiTableIsDejas(code[i+1:end], exe) {
			out = append(out, lines[i:end]...)
			i = end
			continue
		}
		// The table and the blank lines after it; a comment the reader wrote
		// above the next hook stays.
		for end < len(lines) && strings.TrimSpace(lines[end]) == "" {
			end++
		}
		i = end
	}
	return strings.Join(out, "\n")
}

// kimiLegacyHooks are lines an older kimi-auto wrote: PreCompact ran
// without --harness before #4793.
var kimiLegacyHooks = []struct {
	event, matcher string
	args           []string
}{
	{"PreCompact", "", []string{"hook-precompact"}},
}

func kimiTableIsDejas(body []string, exe string) bool {
	keys := map[string]string{}
	for _, line := range body {
		if strings.TrimSpace(line) == "" {
			continue
		}
		k, v, ok := tomlLineKeyValue(line)
		// An inline comment is the reader's, and the table with it.
		if !ok || strings.TrimSpace(tomlCode(line)) != strings.TrimSpace(line) {
			return false
		}
		if u, err := strconv.Unquote(v); err == nil {
			v = u
		} else if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
			v = v[1 : len(v)-1]
		}
		keys[k] = v
	}
	if keys["timeout"] != "30" {
		return false
	}
	for _, h := range append(kimiHooks, kimiLegacyHooks...) {
		want := 3
		if h.matcher != "" {
			want = 4
		}
		if len(keys) == want && keys["event"] == h.event && keys["matcher"] == h.matcher &&
			hookCommandKindOf(keys["command"], hookRun(exe, h.args...)) == hookDejas {
			return true
		}
	}
	return false
}

// installKimiMCP writes mcp.json. Kimi Code 2.2 reads only `enabled` on the
// entry, so a "disabled": true kept from the reader is not called off.
func installKimiMCP(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.KimiConfigDir(), "mcp.json")
	res, err := installMCPJSON(path, exe, uninstall)
	if err == nil && !uninstall && !dejaEntrySwitchedOff("kimi", path) {
		res.Note = dropNotePart(res.Note, switchedOffNote)
	}
	return res, err
}

// kimiAutoPreflight runs the refusals of kimi-auto's config.toml and tui.toml
// halves before mcp.json is written: a read-only one was refused after the
// files before it had been written, and the target was left half wired.
func kimiAutoPreflight(exe string, uninstall bool) error {
	// The path the writers will name, without writing the launcher yet. Both
	// files are checked against that one path: passing a file on the binary
	// while the writers name the launcher let config.toml be rewritten before
	// tui.toml was refused (#5309).
	if !uninstall {
		exe = preflightHookExe(exe)
	}
	path := filepath.Join(sources.KimiConfigDir(), "config.toml")
	old, next, err := kimiHooksUpdate(path, exe, uninstall)
	if err != nil {
		return err
	}
	if refusal := writableIfChanged(path, old, next); refusal != nil {
		return refusal
	}
	// Not on the way out: uninstall runs the plain kimi target too, which
	// removes mcp.json whatever this says.
	if uninstall {
		return nil
	}
	tui := filepath.Join(sources.KimiConfigDir(), "tui.toml")
	cmd := hookRun(exe, "statusline")
	old, next, _, err = tomlStatuslineUpdate(tui, "status_line", kimiStatuslineBlock(cmd), cmd, false)
	if err != nil || next == nil {
		return err
	}
	return writableIfChanged(tui, old, next)
}

// writableIfChanged is the read-only refusal writeIfChanged would give, asked
// before anything is written.
func writableIfChanged(path string, old, next []byte) error {
	next = matchFinalNewline(old, matchLineEndings(old, next))
	if bytes.Equal(old, next) {
		return nil
	}
	return configWritable(path)
}

// kimiHookEntry is one marked block. Every block carries the marker, so
// removeKimiHookBlock takes them all and leaves a hand-written hook alone.
func kimiHookEntry(event, command string) string {
	return kimiHookMarker + "\n[[hooks]]\nevent = " + strconv.Quote(event) +
		"\ncommand = " + strconv.Quote(command) + "\ntimeout = 30\n"
}

func kimiHookEntryMatching(event, matcher, command string) string {
	return kimiHookMarker + "\n[[hooks]]\nevent = " + strconv.Quote(event) +
		"\nmatcher = " + strconv.Quote(matcher) +
		"\ncommand = " + strconv.Quote(command) + "\ntimeout = 30\n"
}

// removeKimiHookBlock drops our marked entry and nothing else: the next table
// header ends it, so a hand-written [[hooks]] below survives.
func removeKimiHookBlock(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != kimiHookMarker {
			out = append(out, lines[i])
			continue
		}
		i++ // skip the marker
		if i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "[[hooks]]") {
			i++
		}
		// deja's block ends at the next table header or the next comment. It
		// writes no comments inside its own block, so a `#` line is always
		// someone else's — running past one deleted the note a user had
		// written above their next hook, and swallowed the marker of a second
		// deja block, leaving that block behind unmarked and running (#1699).
		for i < len(lines) {
			t := strings.TrimSpace(lines[i])
			if strings.HasPrefix(t, "[") || strings.HasPrefix(t, "#") {
				break
			}
			i++
		}
		i-- // the loop's own i++ steps onto the next table header
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
}

// dejaHookEntry reports whether a settings.json hook entry runs deja. Used to
// retire wiring written by an older version under an event we abandoned.
func dejaHookEntry(entry map[string]any) bool {
	inner, _ := entry["hooks"].([]any)
	for _, h := range inner {
		m, _ := h.(map[string]any)
		cmd, _ := m["command"].(string)
		// Any deja subcommand counts: the point is to find wiring we wrote,
		// whatever the old binary called.
		// Both tool subcommands are spelled out: the match wants the whole
		// token, so "hook-tool" does not find "hook-tool-after".
		for _, sub := range []string{"hook-context", "hook-prompt", "hook-precompact", "hook-goose", "hook-antigravity", "hook-codewhale",
			"hook-tool", "hook-tool-after", "hook-spawn", "hook-session-end"} {
			if isDejaHookCommand(cmd, "deja "+sub) {
				return true
			}
		}
	}
	return false
}
