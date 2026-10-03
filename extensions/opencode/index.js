// opencode-deja — the sessions you already have, inside opencode.
//
// opencode remembers its own sessions. This plugin answers the other question:
// what you did in Claude Code, Codex, Cursor, Gemini and thirty-one more agents on
// this machine, including months before deja existed. The index is deja's; this
// file is the seam: six tools the model can call, plus recall that arrives
// without being asked for.
//
// This is the 1.x plugin. opencode 2.x loads server.js, which runs this same
// function and wires its hooks to 2.x's seams.

import { createRequire } from "node:module"
import { execFile } from "node:child_process"
import { promisify } from "node:util"
import { homedir } from "node:os"
import { join } from "node:path"
import { accessSync, constants, existsSync, readFileSync } from "node:fs"

import {
  argv,
  clampLimit,
  cliPluginPath,
  configPaths,
  contextText,
  contributions,
  lastUserText,
  mcpWired,
  TOOL_SPECS,
  zodTools,
} from "./lib.js"

const require = createRequire(import.meta.url)
const run_ = promisify(execFile)

const WINDOWS = process.platform === "win32"
const PLATFORM = WINDOWS ? "windows" : process.platform
const ARCH = process.arch === "x64" ? "amd64" : process.arch
const EXE = WINDOWS ? "deja.exe" : "deja"
// How many turns of a session will ask again after being answered with
// nothing. Three covers the moments that pass — a locked index, a call that
// timed out, an upgrade replacing the binary — without turning a store that
// genuinely holds nothing into a shell-out on every turn.
const emptyRetries = 3

// The tool helper is a peer of the host, not of us: it is identity plus a zod
// re-export. Without it the hooks still run and only the model-facing tools are
// skipped — a missing peer must not take the plugin down.
let tool = null
try {
  ;({ tool } = await import("@opencode-ai/plugin"))
} catch {}

// wellKnown lists the places a user's own install lands. PATH is the normal
// answer; these cover the case where opencode was started from a launcher with
// a PATH that never sourced a shell profile.
function wellKnown() {
  const home = homedir()
  if (WINDOWS) {
    const local = process.env.LOCALAPPDATA || join(home, "AppData", "Local")
    return [join(local, "deja", "bin", EXE), join(home, ".local", "bin", EXE)]
  }
  return [
    join(home, ".local", "bin", EXE),
    "/usr/local/bin/deja",
    "/opt/homebrew/bin/deja",
    "/usr/bin/deja",
  ]
}

// resolveDeja picks the binary in the order a user would expect: what they
// pointed at, then the deja they installed themselves and keep current with
// `deja update` or brew, and only last the copy npm brought along with this
// plugin. Pinning them to our bundled copy would freeze their memory at
// whatever version this package was released against.
function resolveDeja(setting) {
  const candidates = [setting, process.env.DEJA_BIN, EXE, ...wellKnown()]
  try {
    candidates.push(require.resolve(`@vshulcz/deja-vu-${PLATFORM}-${ARCH}/bin/${EXE}`))
  } catch {}
  for (const candidate of candidates) {
    if (!candidate) continue
    if (candidate.includes("/") || candidate.includes("\\")) {
      try {
        accessSync(candidate, constants.X_OK)
      } catch {
        continue
      }
    }
    return candidate
  }
  // Nothing to check further; keep the plain name so a failure names the thing
  // that is missing rather than a path nobody chose.
  return EXE
}

// deja is asked for text, never for control flow: memory is optional
// everywhere, and a throw here would end someone's turn.
async function run(bin, args, input, cwd, timeout = 20000) {
  try {
    const child = run_(bin, args, {
      encoding: "utf8",
      timeout,
      cwd,
      maxBuffer: 8 * 1024 * 1024,
    })
    if (input !== undefined) {
      // deja can exit before it reads stdin; unhandled, the EPIPE that
      // follows would take opencode down. The generated plugin does the same.
      child.child.stdin.on("error", () => {})
      child.child.stdin.end(input)
    }
    const { stdout } = await child
    return stdout.trim()
  } catch {
    return ""
  }
}

const NOTHING = "Nothing in this machine's history matches that."
const MISSING =
  "deja is not installed on this machine, so there is no history to search. " +
  "Install it with: curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh"

// installerWiring reads what `deja install` left on disk for opencode: the MCP
// server in the config, and the plugin file `--auto` writes. Both reads are
// wrapped — a config we cannot read means "not wired", so the worst case is
// this package doing the work twice rather than not at all.
function installerWiring() {
  const wiring = { mcp: false, recall: false }
  try {
    wiring.recall = existsSync(cliPluginPath(process.env, homedir()))
  } catch {}
  try {
    for (const path of configPaths(process.env, homedir())) {
      if (!existsSync(path)) continue
      if (mcpWired(readFileSync(path, "utf8"))) {
        wiring.mcp = true
        break
      }
    }
  } catch {}
  return wiring
}

export const DejaPlugin = async ({ client, directory }, options = {}) => {
  const config = options || {}
  const bin = resolveDeja(typeof config.bin === "string" ? config.bin : "")
  const cwd = directory || process.cwd()
  // Every call runs in the session's own directory: deja ranks by project.
  const ask = (args, input, timeout) => run(bin, args, input, cwd, timeout)

  // Whether the binary answers at all is asked once. Without it every tool
  // would report an empty history to someone who simply never installed deja,
  // which reads as "you have no past" rather than "nothing is here to read it".
  const installed = (await ask(["version"], undefined, 5000)) !== ""
  const answer = (text) => (installed ? text || NOTHING : MISSING)

  const digests = new Map()
  const told = new Set()
  // How many times this session has been answered with nothing. An empty
  // answer is cached so the plugin does not shell out every turn, but the
  // reasons for it are not alike: no history is permanent, while a locked
  // index, a timed-out call or a binary being replaced mid-`deja update` are
  // over by the next turn. Cached alike, one bad moment cost the session all
  // of its memory — every later turn read the emptiness rather than asking
  // again. Counted, so a store that really is empty is still only asked a few
  // times.
  const empties = new Map()
  // Sessions between their compacting hook and the summary request.
  const compacting = new Set()
  // Every session the per-prompt hook stamped live. opencode never says a
  // session ended, so without ending them here the stamp sat out its whole
  // window and the next session's MCP recall left a finished one out (#4546).
  const live = new Set()
  const endSession = (id) => ask(["hook-session-end"], JSON.stringify({ session_id: id }), 5000)
  // The session that spawned each one, asked of opencode once. A task
  // sub-agent's digest and recall led with its parent, which is live and
  // asking through it (#4548).
  const parents = new Map()
  const parentOf = async (id) => {
    if (!id) return ""
    if (!parents.has(id)) {
      let parent = ""
      try {
        const res = await client?.session?.get?.({ path: { id } })
        parent = res?.data?.parentID || ""
      } catch {}
      parents.set(id, parent)
    }
    return parents.get(id)
  }

  const hooks = {}

  // What `deja install` already wired for opencode is not repeated here. It
  // writes an MCP server (the same six answers, under their own names) and,
  // with --auto, a plugin file of its own that opencode loads beside this
  // package — verified with `opencode debug config`, which resolves both into
  // one plugin list. Whatever the installer wrote wins, because that is the
  // copy `deja install` keeps current; this package fills the gaps.
  const adds = contributions(installerWiring(), config)

  // The six tools, described once. opencode 1.x takes them through its zod
  // helper and 2.x as JSON Schema (server.js), so the arguments are plain
  // data and each side builds its own.
  const specs = {
    deja_recall: {
      description:
        "Search this machine's own past AI coding sessions — every agent used on it, including months before deja was installed. Use before debugging an error or re-implementing anything that may already exist. Match on the most specific token available: an exact error string, function name, file path or flag.",
      args: {
        query: { type: "string", description: "Specific tokens to match. Several words are ANDed." },
        limit: { type: "number", optional: true, description: "How many sessions to return. Default 5." },
      },
      async execute(args) {
        const limit = String(clampLimit(args.limit))
        return answer(await ask(argv("search", ["--json", "--limit", limit], args.query)))
      },
    },
    deja_session: {
      description:
        "A full digest of the single best-matching past session — what was tried, what was decided, what it cost. Use after deja_recall when the reasoning behind an earlier decision matters, not just that it happened.",
      args: {
        query: { type: "string", description: "A query, or a session id prefix returned by deja_recall." },
      },
      async execute(args) {
        return answer(await ask(argv("ctx", [], args.query)))
      },
    },
    deja_blame: {
      description:
        "The past sessions that discussed a file, so you know why it is shaped the way it is before editing, refactoring or deleting it. Session history, not git authorship.",
      args: {
        path: { type: "string", description: "Path to the file, absolute or relative to the project." },
      },
      async execute(args) {
        return answer(await ask(argv("blame", ["--json"], args.path)))
      },
    },
    deja_fix: {
      description:
        "What this machine ran after that same error before, in the sessions where the error did not come back. Paste the failing output verbatim rather than a paraphrase — the match is on the error's own words.",
      args: {
        error: { type: "string", description: "The failing output, copied as it was printed." },
      },
      async execute(args) {
        return answer(await ask(argv("fix", [], args.error)))
      },
    },
    deja_how: {
      description:
        "The real invocation this machine uses for a build, test, deploy or script, with the flags it actually ran, ordered by how many sessions ran it. A guessed command is plausible and fails on this setup.",
      args: {
        what: { type: "string", description: "The thing to run: a tool, a task, a script name." },
      },
      async execute(args) {
        return answer(await ask(argv("how", [], args.what)))
      },
    },
    deja_remember: {
      description:
        "Store one durable decision once it is settled, as a single self-contained fact that will make sense months later. Not transcripts, not a summary of the conversation, and not anything already obvious from the code.",
      args: {
        text: { type: "string", description: "The decision, in one or two sentences, with the reason it was taken." },
      },
      async execute(args) {
        const written = await ask(argv("remember", [], args.text))
        if (!installed) return MISSING
        return written || "deja did not record that."
      },
    },
  }

  if (adds.tools) {
    // Under a symbol, so 1.x, which reads its hooks by name, never sees it.
    hooks[TOOL_SPECS] = specs
    if (tool) hooks.tool = zodTools(tool, specs)
  }

  if (!adds.recall) return hooks

  // opencode has no session-start hook. The system prompt is assembled on
  // every request, so the session digest is fetched once and pushed there —
  // the same place Claude Code's SessionStart output lands.
  hooks["experimental.chat.system.transform"] = async (input, output) => {
    try {
      // The summary request is the last one a compaction makes, and the digest
      // has no business in it: asked here, deja would hand the summariser the
      // recovery packet meant for the turn after.
      if (input?.sessionID && compacting.delete(input.sessionID)) return
      const key = input?.sessionID || "default"
      if (!digests.has(key)) {
        // The session id, as the plugin `deja install opencode-auto` writes
        // sends it: without it deja hands the session its own work back and
        // never reads the digest cached for it (#4273). The parent rides
        // along so the digest leaves it out too.
        const payload = { session_id: input?.sessionID || "", parent_session_id: await parentOf(input?.sessionID), cwd }
        const { context, receipt } = contextText(await ask(["hook-context"], JSON.stringify(payload), 30000))
        digests.set(key, context)
        // The receipt is the only sign the user gets that memory arrived. Once
        // per session: repeating it every turn is wallpaper. The hook's own
        // output is model context, so the toast is the only channel for it.
        if (receipt && !told.has(key)) {
          told.add(key)
          await client.tui.showToast({
            body: { message: receipt, variant: "info", duration: 6000 },
          })
        }
      }
      const context = digests.get(key)
      if (context) {
        // Fold into the first system entry, as the generated plugin does.
        // opencode sends each entry as its own system message, and a backend
        // whose template allows one (vLLM or SGLang serving Qwen) rejects the
        // request: "System message must be at the beginning." (#4058)
        if (output.system.length) output.system[0] = context + "\n\n" + output.system[0]
        else output.system.push(context)
        return
      }
      // Nothing recalled: this machine has no history yet, the first index is
      // still building, or the call did not get through. The build is the only
      // one worth saying out loud, and saying it drops the empty answer so the
      // next turn asks again.
      const asks = (empties.get(key) || 0) + 1
      empties.set(key, asks)
      if (asks < emptyRetries) digests.delete(key)
      if (told.has(key)) return
      const status = await ask(["warmup-status"], undefined, 5000)
      if (!status) return
      told.add(key)
      digests.delete(key)
      await client.tui.showToast({
        body: { message: status, variant: "info", duration: 6000 },
      })
    } catch {
      // memory is optional: never break the session over it
    }
  }

  // Per-prompt recall, the relevance pass Claude Code gets on
  // UserPromptSubmit: the digest above is ranked by the project, this is ranked
  // by what the user just asked. Silent when nothing matches.
  hooks["experimental.chat.messages.transform"] = async (input, output) => {
    try {
      // The compaction's own request runs this transform right after the
      // compacting hook. Recall there would land in the summariser's input and
      // the recovery packet would count as delivered to a turn that never saw it.
      const owner = (output?.messages || []).find((m) => m?.info?.sessionID)?.info?.sessionID
      if (owner && compacting.has(owner)) return
      const { parts, prompt, sessionID } = lastUserText(output?.messages)
      // A turn with no text, an image alone, still goes to hook-prompt: that
      // call is what stamps the session live again after session.idle ended
      // it, and without it the turn's MCP recall could return the session to
      // itself (#4573). sessionID is empty when there is no user message.
      if (!prompt && !sessionID) return
      // The session id lets recall skip what it already showed this session.
      // Without it every message re-injects the same block — measured on a real
      // store, half of all injections were a word-for-word repeat.
      const key = input?.sessionID || sessionID || ""
      if (key) live.add(key)
      const raw = await ask(["hook-prompt"], JSON.stringify({ prompt, session_id: key, parent_session_id: await parentOf(key), cwd }))
      if (!prompt || !raw) return
      const extra = JSON.parse(raw)?.hookSpecificOutput?.additionalContext
      if (!extra) return
      parts[parts.length - 1].text += "\n\n" + extra
    } catch {
      // memory is optional: never break the session over it
    }
  }

  // Compaction is about to summarise the session away. The turns are still in
  // opencode's store, so deja reads them by the session id and keeps a
  // recovery packet — commands, how they went, what was open — for the next
  // turn, as Claude Code gets through PreCompact.
  hooks["experimental.session.compacting"] = async (input) => {
    try {
      const sessionID = input?.sessionID || ""
      if (sessionID) compacting.add(sessionID)
      await ask(["hook-precompact"], JSON.stringify({ session_id: sessionID, cwd, harness: "opencode" }), 60000)
    } catch {
      // memory is optional: never break a compaction over it
    }
  }

  // A spawned agent gets none of the above: the system prompt was built for the
  // session that spawned it, and the per-prompt pass fires on what the user
  // typed, which a subagent never does. Its instructions are the one thing that
  // reaches it, so recall goes in there.
  hooks["tool.execute.before"] = async (input, output) => {
    try {
      if (input?.tool !== "task") return
      const args = output?.args
      if (!args?.prompt) return
      const payload = {
        hook_event_name: "PreToolUse",
        tool_name: "Task",
        tool_input: { prompt: args.prompt },
        session_id: input.sessionID || "",
        cwd,
      }
      const raw = await ask(["hook-tool"], JSON.stringify(payload))
      if (!raw) return
      const next = JSON.parse(raw)?.hookSpecificOutput?.updatedInput?.prompt
      if (next) args.prompt = next
    } catch {
      // memory is optional: never break a spawn over it
    }
  }

  // The moment a command fails is the one an agent never thinks to ask about,
  // and tool.execute.after is the only seam opencode gives for it. The hook
  // returns nothing to inject, so the line is folded into the tool output,
  // which the next request carries as the tool result.
  hooks["tool.execute.after"] = async (input, output) => {
    try {
      // A file's history, at the read before the edit. opencode has no seam
      // that can inject before a file action — tool.execute.before can only
      // rewrite the arguments — so the line goes out the way the failure line
      // does: appended to the tool's own output, which the next request
      // carries as the tool result. Without this, what deja knows at the
      // point of an action reached every harness except this one.
      //
      // Matched on the argument rather than the tool name: a tool carrying a
      // file path is a file action whatever this version calls it, and a name
      // deja does not recognise is answered with silence.
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
        const fraw = await ask(["hook-tool"], JSON.stringify(fpayload))
        if (!fraw) return
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
      const raw = await ask(["hook-tool-after"], JSON.stringify(payload))
      if (!raw) return
      const extra = JSON.parse(raw)?.hookSpecificOutput?.additionalContext
      if (extra) output.output = text + "\n\n" + extra
    } catch {
      // memory is optional: never break a tool call over it
    }
  }

  // A turn is over when opencode publishes session.idle, and the next prompt
  // stamps the session again before the model can ask anything, so the session
  // is history to everyone else in between.
  hooks.event = async ({ event }) => {
    try {
      const id = event?.type === "session.idle" ? event.properties?.sessionID : ""
      if (!id) return
      await endSession(id)
      // Ended, so dispose does not spawn deja for it again on the way out.
      live.delete(id)
    } catch {
      // memory is optional: never break the session over it
    }
  }

  // `opencode run` exits without waiting for the event above, and it does wait
  // for dispose: a one-shot run ends what it started here.
  hooks.dispose = async () => {
    for (const id of live) await endSession(id)
    live.clear()
  }

  return hooks
}
