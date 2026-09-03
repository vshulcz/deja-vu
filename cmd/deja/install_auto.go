package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
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
// deja — SessionStart alone, where there are now three — was reported as wired
// on the strength of the trust entry, and the two events added since reached
// nobody.
var codexHookWiring = []struct{ Event, Sub, Matcher string }{
	// SessionStart carries the project digest.
	{"SessionStart", "hook-context", "startup|resume"},
	// PreToolUse carries the prior decision for the file or command about to
	// change, scoped to the tools that run a command or change a file (codex
	// edits via apply_patch) so the hook does not spawn on every read.
	{"PreToolUse", "hook-tool", "Bash|apply_patch"},
	// And the fix pair when a command fails, which is the moment an agent never
	// thinks to ask for it.
	{"PostToolUse", "hook-tool-after", "Bash"},
}

func installCodexHooks(exe string, uninstall bool) (installResult, error) {
	// Use CodexHome() (honours CODEX_HOME / DEJA_CODEX_ROOT) rather than a raw
	// ~/.codex join, so a sandboxed install stays sandboxed and a non-default
	// codex home gets its hooks written where codex actually reads them. Every
	// other codex path already goes through it (e.g. doctor.go). See #850.
	path := filepath.Join(sources.CodexHome(), "hooks.json")
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return installResult{}, configParseError(path, err)
	}
	for _, h := range codexHookWiring {
		updateCodexHook(root, h.Event, exe+" "+h.Sub, h.Matcher, uninstall)
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
		}
		kept = append(kept, entryAny)
	}
	if !uninstall && !found {
		h := map[string]any{"type": "command", "command": cmd, "timeout": 10}
		// Codex surfaces statusMessage in its hook run summary.
		if msg := hookStatusMessage(event); msg != "" {
			h["statusMessage"] = msg
		}
		kept = append(kept, map[string]any{"matcher": matcher, "hooks": []any{h}})
	}
	if len(kept) == 0 {
		delete(hooks, event)
	} else {
		hooks[event] = kept
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

// opencode has no session-start hook; its plugin API can push text onto the
// system prompt per request. The generated plugin shells out to
// `deja hook-context --plain` once per session and caches the result.
func installOpencodePlugin(exe string, uninstall bool) (installResult, error) {
	dir := filepath.Join(opencodeConfigHome(), "opencode", "plugins")
	path := filepath.Join(dir, "deja.js")
	if uninstall {
		if _, err := os.Stat(path); err != nil {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		if err := os.Remove(path); err != nil {
			return installResult{}, err
		}
		return installResult{Path: path, Action: "removed"}, nil
	}
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	next := []byte(opencodePluginJS(exe))
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

func opencodePluginJS(exe string) string {
	// opencode's hook output goes into the model's context, so a progress
	// note cannot ride along with it — it would be prompt noise. The TUI
	// toast is the channel meant for the human, so the build announces itself
	// there instead, once per session so it does not nag on every turn.
	return fmt.Sprintf(`// generated by deja install — safe to delete; regenerate with: deja install opencode-auto
export const DejaRecall = async ({ $, client }) => {
  const cache = new Map()
  const told = new Set()
  // How many times this session was answered with nothing. The empty answer
  // is cached so the plugin does not shell out every turn, but its reasons
  // are not alike: no history is permanent, while a locked index, a call that
  // did not get through, or an upgrade replacing the binary are over by the
  // next turn. Cached alike, one bad moment cost the session all of its
  // memory. Counted, so a store that really is empty is still asked only a
  // few times.
  const empties = new Map()
  const emptyRetries = 3
  return {
    "experimental.chat.system.transform": async (input, output) => {
      try {
        const key = input.sessionID || "default"
        if (!cache.has(key)) {
          const raw = await $%s%q %s%s.text()
          let ctx = "", receipt = ""
          try {
            const parsed = JSON.parse(raw)
            ctx = parsed?.hookSpecificOutput?.additionalContext || ""
            receipt = parsed?.systemMessage || ""
          } catch {
            ctx = raw.trim()
          }
          cache.set(key, ctx)
          // The receipt is the only sign the user gets that memory arrived.
          // Once per session: repeating it every turn is wallpaper.
          if (receipt && !told.has(key)) {
            told.add(key)
            await client.tui.showToast({ body: { message: receipt, variant: "info", duration: 6000 } })
          }
        }
        const ctx = cache.get(key)
        if (ctx) {
          // Fold into the first system block rather than appending a second
          // one. An OpenAI-compatible endpoint that requires the system
          // message to come first rejects the whole request otherwise, so
          // installing deja made opencode fail every turn against a local
          // model: "Not Found: System message must be at the beginning."
          if (output.system.length) output.system[0] = ctx + "\n\n" + output.system[0]
          else output.system.push(ctx)
          return
        }
        // Nothing to recall: there is no history yet, the first index is still
        // being built, or the call did not get through. Only the build is worth
        // saying out loud; the rest is worth asking again.
        const asks = (empties.get(key) || 0) + 1
        empties.set(key, asks)
        if (asks < emptyRetries) cache.delete(key)
        if (told.has(key)) return
        const status = (await $%s%q %s%s.text()).trim()
        if (!status) return
        told.add(key)
        cache.delete(key)
        await client.tui.showToast({ body: { message: status, variant: "info", duration: 6000 } })
      } catch {
        // memory is optional: never break the session over it
      }
    },
    // Compaction is about to throw away the working transcript. Claude Code
    // gets the same treatment through PreCompact: index what exists now, so
    // the session survives in memory even after the window is collapsed.
    "experimental.session.compacting": async () => {
      try {
        await $%s%q hook-precompact%s.quiet()
      } catch {
        // memory is optional: never break a compaction over it
      }
    },
    // Per-prompt recall, the same relevance pass Claude Code gets on
    // UserPromptSubmit: the session digest is ranked by the project, this is
    // ranked by what the user just asked. Silent when nothing matches.
    "experimental.chat.messages.transform": async (input, output) => {
      try {
        const msgs = output.messages || []
        let last
        for (let i = msgs.length - 1; i >= 0; i--) {
          if (msgs[i]?.info?.role === "user") { last = msgs[i]; break }
        }
        if (!last) return
        const parts = (last.parts || []).filter((p) => p?.type === "text" && p.text)
        const prompt = parts.map((p) => p.text).join("\n").trim()
        if (!prompt) return
        // The session id travels with the payload so recall can skip what it
        // already showed this session. Without it every message re-injects the
        // same block: measured on a real store, half of all injections were a
        // word-for-word repeat, and all but five of those came within a minute.
        const sessionID = input?.sessionID || last?.info?.sessionID || ""
        const raw = await $%secho ${JSON.stringify({ prompt, session_id: sessionID })} | %s%q hook-prompt%s.text()
        if (!raw.trim()) return
        const extra = JSON.parse(raw)?.hookSpecificOutput?.additionalContext
        if (!extra) return
        parts[parts.length - 1].text += "\n\n" + extra
      } catch {
        // memory is optional: never break the session over it
      }
    },
    // A spawned agent gets none of the above: the system prompt is built for
    // the session that spawned it and the per-prompt pass fires on what the
    // user typed, which a subagent never does. Its instructions are the one
    // thing that reaches it, so recall goes in there.
    "tool.execute.before": async (input, output) => {
      try {
        if (input?.tool !== "task") return
        const args = output?.args
        if (!args?.prompt) return
        // One encoding, not two. Stringified again on the way into the shell,
        // deja received a JSON string where it expects an object, read nothing
        // out of it and answered with silence — so a spawned agent in opencode
        // has been starting with no memory at all.
        const payload = {
          hook_event_name: "PreToolUse",
          tool_name: "Task",
          tool_input: { prompt: args.prompt },
          session_id: input.sessionID || "",
        }
        const raw = await $%secho ${JSON.stringify(payload)} | %s%q hook-tool%s.text()
        if (!raw.trim()) return
        const next = JSON.parse(raw)?.hookSpecificOutput?.updatedInput?.prompt
        if (next) args.prompt = next
      } catch {
        // memory is optional: never break a spawn over it
      }
    },
  }
}
`, "`", exe, "hook-context", "`", "`", exe, "warmup-status", "`", "`", exe, "`", "`", "", exe, "`", "`", "", exe, "`")
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

// Qwen took two wrong readings to get right. Its `timeout` is MILLISECONDS,
// like Gemini's — the 10 deja used to write killed the hook ten milliseconds
// in, which is indistinguishable from a harness that has no hooks. And while
// SessionStart does fire, only UserPromptSubmit consumes additionalContext
// (appendUserPromptExpansionAdditionalContext), and Qwen checks that the
// hookEventName in the reply matches the event — so the SessionStart-shaped
// output of `hook-context` was dropped without a word.
func installQwenAuto(exe string, uninstall bool) (installResult, error) {
	// Older deja wrote SessionStart here, which qwen never consumed. Naming
	// it as retired means an upgrade removes the dead entry instead of
	// leaving qwen to run a hook that answers into the void.
	return installSettingsHookRetiring(
		filepath.Join(sources.QwenConfigDir(), "settings.json"),
		"UserPromptSubmit", "", 60000, exe+" hook-prompt", uninstall,
		map[string]bool{"SessionStart": true})
}

// installSettingsHook merges one hook entry into a settings.json that the
// host also uses for everything else, leaving the rest of the file alone.
func installSettingsHook(path, event, matcher string, timeout int, exe string, uninstall bool) (installResult, error) {
	return installSettingsHookCmd(path, event, matcher, timeout, exe+" hook-context", uninstall)
}

func installSettingsHookCmd(path, event, matcher string, timeout int, cmd string, uninstall bool) (installResult, error) {
	return installSettingsHookRetiring(path, event, matcher, timeout, cmd, uninstall, nil)
}

// installSettingsHookRetiring also drops deja hooks under events this harness
// no longer uses. Without it a generator fix ships and the old, dead entry
// keeps firing next to the new one for everyone who installed before.
func installSettingsHookRetiring(path, event, matcher string, timeout int, cmd string, uninstall bool, retire map[string]bool) (installResult, error) {
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
		source = []byte(stripJSONComments(string(old)))
	}
	if len(bytes.TrimSpace(source)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(source, &root); err != nil {
		return installResult{}, configParseError(path, err)
	}
	before := ""
	if jsonc {
		b, err := json.Marshal(root)
		if err != nil {
			return installResult{}, err
		}
		before = string(b)
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	for name := range retire {
		if name == event {
			continue
		}
		old, _ := hooks[name].([]any)
		var survivors []any
		for _, entryAny := range old {
			entry, _ := entryAny.(map[string]any)
			if entry != nil && dejaHookEntry(entry) {
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
		after, err := json.Marshal(root)
		if err != nil {
			return installResult{}, err
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

// Kimi Code runs SessionStart hooks, so auto-recall works there too. Its
// config is TOML, not JSON, and the entry is a flat table rather than the
// nested matcher/hooks shape Claude uses.
// Kimi injects the hook's plain stdout, not a JSON field — its structured
// output only carries permission decisions. And only UserPromptSubmit does it:
// a SessionStart hook runs and its output goes nowhere, which is what made
// this look like a harness that cannot take context at all.
func installKimiAuto(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.KimiConfigDir(), "config.toml")
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	s := strings.TrimRight(removeKimiHookBlock(lfText(old)), "\n")
	if !uninstall {
		block := kimiHookMarker + "\n[[hooks]]\nevent = \"UserPromptSubmit\"\ncommand = " +
			strconv.Quote(exe+" hook-prompt --plain") + "\ntimeout = 30\n"
		if s != "" {
			s += "\n\n"
		}
		s += block
	} else if s != "" {
		s += "\n"
	}
	a, err := writeIfChanged(path, old, []byte(s))
	return installResult{Path: path, Action: a}, err
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
		for _, sub := range []string{"hook-context", "hook-prompt", "hook-precompact", "hook-goose", "hook-antigravity"} {
			if isDejaHookCommand(cmd, "deja "+sub) {
				return true
			}
		}
	}
	return false
}
