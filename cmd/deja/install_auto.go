package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

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
var codexHookWiring = []struct{ Event, Sub, Matcher string }{
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
}

func installCodexHooks(exe string, uninstall bool) (installResult, error) {
	exe = hookExeFor(exe, uninstall)
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
		h := map[string]any{"type": "command", "command": cmd, "timeout": 10}
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
	// The launcher, not this binary: a generated plugin is as much a
	// config as a hooks.json, and one that names the build it was
	// installed from stops working the day that build moves (#3682).
	exe = hookExeFor(exe, uninstall)
	dir := filepath.Join(opencodeConfigHome(), "opencode", "plugins")
	path := filepath.Join(dir, "deja.js")
	if uninstall {
		if _, err := os.Stat(path); err != nil {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		if err := os.Remove(path); err != nil {
			return installResult{}, err
		}
		// And the plugins directory deja made for it (#3698).
		pruneCreatedDir(dir)
		return installResult{Path: path, Action: "removed"}, nil
	}
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	next := []byte(opencodePluginJSFor(exe))
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

func opencodePluginJS(exe string) string {
	// OpenCode V2 reads only the module's default export, and it must carry
	// an id and a setup (or effect) function; the V1 named-export shape was
	// rejected outright ("Missing key at [\"default\"]", ref err_bb76d60a),
	// which silently cost every session its auto-recall. The V1 hooks map
	// onto V2 domains: the system and message transforms become session
	// "context" hooks, the compaction notice becomes the "compaction" hook,
	// and the tool pair becomes ctx.tool "execute.before"/"execute.after".
	// The shell helper is no longer handed to the plugin, so the hook
	// launcher is spawned directly with the payload on stdin — one
	// encoding, no quoting.
	//
	// The definition is a plain { id, setup } object rather than
	// Plugin.define(): a local plugin resolves bare specifiers from its own
	// directory, where @opencode/plugin is not installed ("Cannot find
	// package '@opencode/plugin' imported from .../plugins/deja.js"), and
	// define() is the identity function anyway.
	//
	// The toast is gone: a V2 server plugin has no channel to the TUI, so
	// the receipt and the build note have nowhere to be said. The digest
	// itself still rides the recall envelope.
	return fmt.Sprintf(`// generated by deja install — safe to delete; regenerate with: deja install opencode-auto
import { spawn } from "node:child_process"

const HOOK = %q

// runHook runs the deja hook launcher and resolves with its stdout,
// feeding payload on stdin when there is one. Never rejects: memory is
// optional.
const runHook = (subcommand, payload, cwd) =>
  new Promise((resolve) => {
    let child
    try {
      child = spawn(HOOK, [subcommand], cwd ? { cwd } : {})
    } catch {
      resolve("")
      return
    }
    let out = ""
    child.stdout.on("data", (d) => { out += d })
    child.on("error", () => resolve(""))
    child.on("close", () => resolve(out))
    child.stdin.on("error", () => {})
    if (payload !== undefined) child.stdin.write(payload)
    child.stdin.end()
  })

export default {
  id: "deja-recall",
  async setup(ctx) {
    // deja ranks by project, and the project is a directory. The plugin
    // instance's location is the one opencode hands over; without it every
    // call took the server process's cwd, which is where opencode was
    // launched rather than where the work is. Asked from the wrong
    // directory the same question answers out of another project's
    // sessions, and from a directory with no history it answers nothing
    // at all.
    const cwd = ctx.location?.directory || process.cwd()
    const cache = new Map()
    // How many times this session was answered with nothing. The empty
    // answer is cached so the plugin does not shell out every turn, but
    // its reasons are not alike: no history is permanent, while a locked
    // index, a call that did not get through, or an upgrade replacing the
    // binary are over by the next turn. Counted, so a store that really
    // is empty is still asked only a few times.
    const empties = new Map()
    const emptyRetries = 3

    // Session digest: fold into the first system block rather than
    // appending a second one. An OpenAI-compatible endpoint that requires
    // the system message to come first rejects the whole request
    // otherwise, so installing deja made opencode fail every turn against
    // a local model: "Not Found: System message must be at the beginning."
    await ctx.session.hook("context", async (event) => {
      try {
        const key = event.sessionID || "default"
        if (!cache.has(key)) {
          const raw = await runHook("hook-context", undefined, cwd)
          let digest = ""
          try {
            digest = JSON.parse(raw)?.hookSpecificOutput?.additionalContext || ""
          } catch {
            digest = raw.trim()
          }
          cache.set(key, digest)
        }
        const digest = cache.get(key)
        if (digest) {
          if (event.system.length) event.system[0].text = digest + "\n\n" + event.system[0].text
          else event.system.push({ type: "text", text: digest })
          return
        }
        const asks = (empties.get(key) || 0) + 1
        empties.set(key, asks)
        if (asks < emptyRetries) cache.delete(key)
      } catch {
        // memory is optional: never break the session over it
      }
    })

    // Per-prompt recall: ranked by what the user just asked, not just by
    // the project. The session id travels with the payload so recall can
    // skip what it already showed this session; without it every message
    // re-injects the same block.
    await ctx.session.hook("context", async (event) => {
      try {
        const msgs = event.messages || []
        let last
        for (let i = msgs.length - 1; i >= 0; i--) {
          if (msgs[i]?.role === "user") { last = msgs[i]; break }
        }
        if (!last) return
        const parts = (last.content || []).filter((p) => p?.type === "text" && p.text)
        const prompt = parts.map((p) => p.text).join("\n").trim()
        if (!prompt) return
        const payload = { prompt, session_id: event.sessionID || "", cwd }
        const raw = await runHook("hook-prompt", JSON.stringify(payload))
        if (!raw.trim()) return
        const extra = JSON.parse(raw)?.hookSpecificOutput?.additionalContext
        if (!extra) return
        parts[parts.length - 1].text += "\n\n" + extra
      } catch {
        // memory is optional: never break the session over it
      }
    })

    // Compaction is about to throw away the working transcript: index
    // what exists now, so the session survives in memory even after the
    // window is collapsed.
    await ctx.session.hook("compaction", async () => {
      try {
        await runHook("hook-precompact", undefined, cwd)
      } catch {
        // memory is optional: never break a compaction over it
      }
    })

    // A spawned agent gets none of the above: its instructions are the
    // one thing that reaches it, so recall goes in there. The payload is
    // encoded once — deja expects an object, not a quoted string.
    await ctx.tool.hook("execute.before", async (event) => {
      try {
        if (event.tool !== "subagent") return
        const args = event.input
        if (!args?.prompt) return
        const payload = {
          hook_event_name: "PreToolUse",
          tool_name: "Task",
          tool_input: { prompt: args.prompt },
          session_id: event.sessionID || "",
          cwd,
        }
        const raw = await runHook("hook-tool", JSON.stringify(payload))
        if (!raw.trim()) return
        const next = JSON.parse(raw)?.hookSpecificOutput?.updatedInput?.prompt
        if (next) event.input = { ...args, prompt: next }
      } catch {
        // memory is optional: never break a spawn over it
      }
    })

    // The moment a command fails is the one an agent never thinks to ask
    // about. The line cannot be handed to the model directly, so it is
    // folded into the tool's own output, which the next request carries
    // as the tool result. A failed command still reports "completed"
    // here: the failure lives in the text and the exit metadata, not the
    // status.
    await ctx.tool.hook("execute.after", async (event) => {
      try {
        // A file's history, at the read before the edit: the before-seam can
        // only rewrite arguments, so this is where the line can reach the
        // model. Matched on the argument, because the tool that carries a file
        // path is a file action whatever this version calls it.
        const fpath = event.input?.filePath || event.input?.path || ""
        if (fpath && event.tool !== "shell" && event.status === "completed") {
          const fres = event.result || {}
          const fcontent = Array.isArray(fres.content) ? fres.content : []
          const fpayload = {
            hook_event_name: "PostToolUse",
            tool_name: event.tool || "read",
            tool_input: { file_path: fpath },
            session_id: event.sessionID || "",
            cwd,
          }
          const fraw = await runHook("hook-tool", JSON.stringify(fpayload))
          if (!fraw.trim()) return
          const fextra = JSON.parse(fraw)?.hookSpecificOutput?.additionalContext
          if (fextra) event.result = { ...fres, content: [...fcontent, { type: "text", text: fextra }] }
          return
        }
        if (event.tool !== "shell" || event.status !== "completed") return
        const result = event.result || {}
        const content = Array.isArray(result.content)
          ? result.content
          : typeof result.content === "string"
            ? [{ type: "text", text: result.content }]
            : typeof result.output === "string"
              ? [{ type: "text", text: result.output }]
              : []
        const text = content.filter((c) => c?.type === "text").map((c) => c.text).join("\n")
        if (!text) return
        const payload = {
          hook_event_name: "PostToolUse",
          tool_name: "bash",
          tool_input: { command: event.input?.command || "" },
          tool_response: { output: text },
          session_id: event.sessionID || "",
          cwd,
        }
        const raw = await runHook("hook-tool-after", JSON.stringify(payload))
        if (!raw.trim()) return
        const extra = JSON.parse(raw)?.hookSpecificOutput?.additionalContext
        if (extra) event.result = { ...result, content: [...content, { type: "text", text: extra }] }
      } catch {
        // memory is optional: never break a tool call over it
      }
    })
  },
}
`, exe)
}

// opencodeVersionMajorReal is the major version of the opencode on PATH, or 0
// when there is no opencode to ask.
var opencodeVersionMajorReal = func() int {
	// The escape hatch for a machine where the binary is not on PATH under
	// that name — a shell alias, a bun run, a wrapper script — and for a test
	// that must not depend on which opencode the developer happens to have.
	if v := os.Getenv("DEJA_OPENCODE_MAJOR"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	exe, err := exec.LookPath("opencode")
	if err != nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "--version").Output()
	if err != nil {
		return 0
	}
	m := regexp.MustCompile(`(\d+)\.\d+\.\d+`).FindSubmatch(out)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return 0
	}
	return n
}

// opencodeVersionMajor is what install asks, indirected so a test can say which
// opencode is installed without one being installed.
var opencodeVersionMajor = opencodeVersionMajorReal

// opencodePluginJSFor is the plugin the installed opencode can actually load.
// The binary on PATH answers it; with no binary to ask, the store does, since a
// 2.0 home has already migrated its tables. Neither answers on a machine where
// opencode has never run, and a fresh install is 2.x.
func opencodePluginJSFor(exe string) string {
	switch major := opencodeVersionMajor(); {
	case major == 1:
		return opencodeLegacyPluginJS(exe)
	case major >= 2:
		return opencodePluginJS(exe)
	}
	if db := sources.OpencodeDB(); db != "" {
		if _, err := os.Stat(db); err == nil && !sources.OpencodeStoreIsV2(db) {
			return opencodeLegacyPluginJS(exe)
		}
	}
	return opencodePluginJS(exe)
}

// opencodeLegacyPluginJS is the 1.x plugin: a named export returning the
// experimental hook table. 1.18.32 refuses the 2.0 file outright — "Plugin
// .../deja.js must default export an object with server()" — so a store still
// on 1.x keeps this one, and nothing is written that the installed opencode
// cannot load.
func opencodeLegacyPluginJS(exe string) string {
	// opencode's hook output goes into the model's context, so a progress
	// note cannot ride along with it — it would be prompt noise. The TUI
	// toast is the channel meant for the human, so the build announces itself
	// there instead, once per session so it does not nag on every turn.
	return fmt.Sprintf(`// generated by deja install — safe to delete; regenerate with: deja install opencode-auto
export const DejaRecall = async ({ $, client, directory }) => {
  // deja ranks by project, and the project is a directory. opencode hands the
  // plugin the session's own; without it every call took the server process's
  // cwd, which is where opencode was launched rather than where the work is.
  // Asked from the wrong directory the same question answers out of another
  // project's sessions, and from a directory with no history it answers
  // nothing at all.
  const cwd = directory || process.cwd()
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
          const raw = await $%scd ${cwd} && %q %s%s.text()
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
        await $%scd ${cwd} && %q hook-precompact%s.quiet()
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
        const raw = await $%secho ${JSON.stringify({ prompt, session_id: sessionID, cwd })} | %s%q hook-prompt%s.text()
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
          cwd,
        }
        const raw = await $%secho ${JSON.stringify(payload)} | %s%q hook-tool%s.text()
        if (!raw.trim()) return
        const next = JSON.parse(raw)?.hookSpecificOutput?.updatedInput?.prompt
        if (next) args.prompt = next
      } catch {
        // memory is optional: never break a spawn over it
      }
    },
    // The moment a command fails is the one an agent never thinks to ask
    // about, and this is the only seam opencode has for it. The hook returns
    // void, so the line cannot be handed to the model directly — it is
    // appended to the tool's own output, which the next request carries as the
    // tool result. Verified against a real run: a string written here arrives
    // in the tool message of the following request.
    "tool.execute.after": async (input, output) => {
      try {
        // A file's history, at the read before the edit. opencode's
        // before-seam can only rewrite a tool's arguments, so the line goes
        // out the way the failure line does: appended to the tool's own
        // output, which the next request carries as the tool result. Matched
        // on the argument rather than the tool name — a tool carrying a file
        // path is a file action whatever this version calls it.
        const fpath = input?.args?.filePath || input?.args?.path || ""
        if (fpath && input?.tool !== "bash") {
          const ftext = output?.output
          if (!ftext) return
          const fpayload = {
            hook_event_name: "PostToolUse",
            tool_name: input?.tool || "read",
            tool_input: { file_path: fpath },
            session_id: input.sessionID || "",
            cwd,
          }
          const fraw = await $%secho ${JSON.stringify(fpayload)} | %s%q hook-tool%s.text()
          if (!fraw.trim()) return
          const fextra = JSON.parse(fraw)?.hookSpecificOutput?.additionalContext
          if (fextra) output.output = ftext + "\n\n" + fextra
          return
        }
        if (input?.tool !== "bash") return
        const text = output?.output
        if (!text) return
        const payload = {
          hook_event_name: "PostToolUse",
          tool_name: "bash",
          tool_input: { command: input?.args?.command || "" },
          tool_response: { output: text },
          session_id: input.sessionID || "",
          cwd,
        }
        const raw = await $%secho ${JSON.stringify(payload)} | %s%q hook-tool-after%s.text()
        if (!raw.trim()) return
        const extra = JSON.parse(raw)?.hookSpecificOutput?.additionalContext
        if (extra) output.output = text + "\n\n" + extra
      } catch {
        // memory is optional: never break a tool call over it
      }
    },
  }
}
`, "`", exe, "hook-context", "`", "`", exe, "warmup-status", "`", "`", exe, "`", "`", "", exe, "`", "`", "", exe, "`", "`", "", exe, "`", "`", "", exe, "`")
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
	// Compaction throws away the blocks this session was shown while the list
	// that stops them repeating outlives them, so without this the memory qwen
	// just lost is the memory recall refuses to send again.
	{"PreCompact", "hook-precompact", ""},
}

// qwenRetiredEvents are events deja used to write for qwen and no longer does.
// The PostToolUse entry fired only after a tool that succeeded, so it looked
// up a repair for commands that did not need one and stayed quiet for the ones
// that did; leaving it behind would keep that cost on every green command.
var qwenRetiredEvents = map[string]bool{"PostToolUse": true}

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
func installSettingsHook(path, event, matcher string, timeout int, exe string, uninstall bool) (installResult, error) {
	return installSettingsHookCmd(path, event, matcher, timeout, hookRun(exe, "hook-context"), uninstall)
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
		source = []byte(jsoncToJSON(string(old)))
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
	exe = hookExeFor(exe, uninstall)
	path := filepath.Join(sources.KimiConfigDir(), "config.toml")
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	s := strings.TrimRight(removeKimiHookBlock(lfText(old)), "\n")
	if !uninstall {
		block := kimiHookEntry("UserPromptSubmit", hookRun(exe, "hook-context", "--plain", "--once")) +
			"\n" + kimiHookEntry("UserPromptSubmit", hookRun(exe, "hook-prompt", "--plain")) +
			// Compaction throws away what the session was shown, and the list
			// that stops those blocks repeating has to go with it. Nothing is
			// read back from this hook: forgetting is a side effect, which is
			// all a fire-and-forget event can carry.
			"\n" + kimiHookEntry("PreCompact", hookRun(exe, "hook-precompact"))
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

// kimiHookEntry is one marked block. Every block carries the marker, so
// removeKimiHookBlock takes them all and leaves a hand-written hook alone.
func kimiHookEntry(event, command string) string {
	return kimiHookMarker + "\n[[hooks]]\nevent = " + strconv.Quote(event) +
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
		for _, sub := range []string{"hook-context", "hook-prompt", "hook-precompact", "hook-goose", "hook-antigravity",
			"hook-tool", "hook-tool-after", "hook-spawn"} {
			if isDejaHookCommand(cmd, "deja "+sub) {
				return true
			}
		}
	}
	return false
}
