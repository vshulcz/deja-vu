// deja-vu for OpenClaw: recall from the coding sessions already on this
// machine before each turn, and three tools to ask the history directly.
import { execFile } from "node:child_process"
import { accessSync, constants, existsSync, readFileSync } from "node:fs"
import { createRequire } from "node:module"
import { homedir } from "node:os"
import { join } from "node:path"
import { promisify } from "node:util"

import {
  accessNote,
  argv,
  configPath,
  contributions,
  createSeenStore,
  createSessionStore,
  directTurn,
  installerPluginPath,
  mcpWired,
  promptText,
  recallArgv,
  recallSilence,
  receiptAllowed,
  receiptLine,
  replyTakes,
  splitHookReply,
  toolCall,
  where,
  withReceipt,
} from "./lib.mjs"

const require = createRequire(import.meta.url)
const run_ = promisify(execFile)

const WINDOWS = process.platform === "win32"
const PLATFORM = WINDOWS ? "windows" : process.platform
const ARCH = process.arch === "x64" ? "amd64" : process.arch
const EXE = WINDOWS ? "deja.exe" : "deja"

const NOTHING = "Nothing in this machine's history matches that."
const MISSING =
  "deja is not installed on this machine, so there is no history to search. " +
  "Install it with: brew install deja-vu (or: go install github.com/vshulcz/deja-vu/cmd/deja@latest)"

// wellKnown lists the places a user's own install lands, for a gateway started
// from a launcher whose PATH never sourced a shell profile.
function wellKnown() {
  const home = homedir()
  if (WINDOWS) {
    const local = process.env.LOCALAPPDATA || join(home, "AppData", "Local")
    return [join(local, "deja", "bin", EXE), join(home, ".local", "bin", EXE)]
  }
  return [join(home, ".local", "bin", EXE), "/usr/local/bin/deja", "/opt/homebrew/bin/deja", "/usr/bin/deja"]
}

// resolveDeja: what the user pointed at, then the deja they installed and keep
// current, and only last the copy npm brought with this package — pinning
// them to our bundled copy would freeze their memory at whatever version this
// package was released against.
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
  return EXE
}

// deja is asked for text, never for control flow: memory is optional
// everywhere, and a throw here would end someone's turn.
async function run(bin, args, input, cwd, timeout = 20000) {
  try {
    const child = run_(bin, args, {
      windowsHide: true,
      encoding: "utf8",
      timeout,
      cwd,
      maxBuffer: 8 * 1024 * 1024,
    })
    if (input !== undefined) {
      // deja can exit before it reads stdin; unhandled, the EPIPE would
      // throw in the gateway.
      child.child.stdin.on("error", () => {})
      child.child.stdin.end(input)
    }
    const { stdout } = await child
    return stdout.trim()
  } catch {
    return ""
  }
}

function parseHookExtEnvelope(raw) {
  const s = String(raw || "").trim()
  if (!s.startsWith("{")) return null
  try {
    const parsed = JSON.parse(s)
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed) && typeof parsed.ok === "boolean") {
      return parsed
    }
  } catch {}
  return null
}

async function runExt(bin, op, payload, cwd, timeout = 20000) {
  let raw = ""
  let timedOut = false
  try {
    const child = run_(bin, ["hook-ext", "--host", "openclaw", "--op", op], {
      windowsHide: true,
      encoding: "utf8",
      timeout,
      cwd,
      maxBuffer: 8 * 1024 * 1024,
    })
    if (payload !== undefined) {
      child.child.stdin.on("error", () => {})
      child.child.stdin.end(JSON.stringify(payload))
    }
    const { stdout } = await child
    raw = stdout
  } catch (err) {
    timedOut = Boolean(err?.killed || err?.signal)
    raw = typeof err?.stdout === "string" ? err.stdout : ""
  }
  return { env: parseHookExtEnvelope(raw), timedOut }
}

// installerWiring reads what `deja install` left for OpenClaw: the MCP server
// in openclaw.json and the plugin `--auto` writes. A read that fails means
// "not wired": the worst case is doing the work twice, not skipping it.
function installerWiring() {
  const wiring = { mcp: false, recall: false }
  try {
    wiring.recall = existsSync(installerPluginPath(process.env, homedir()))
  } catch {}
  try {
    const path = configPath(process.env, homedir())
    if (existsSync(path)) wiring.mcp = mcpWired(readFileSync(path, "utf8"))
  } catch {}
  return wiring
}

const RECALL_SCHEMA = {
  type: "object",
  properties: {
    query: { type: "string", description: "1-3 exact terms: a file path, an error string, a flag or identifier." },
    limit: { type: "integer", description: "How many sessions to return. Default 5." },
    offset: { type: "integer", description: "Sessions to skip, from the offset the previous page named. Default 0." },
  },
  required: ["query"],
}
const FIX_SCHEMA = {
  type: "object",
  properties: { error: { type: "string", description: "The failing output, copied as it was printed." } },
  required: ["error"],
}
const BLAME_SCHEMA = {
  type: "object",
  properties: { path: { type: "string", description: "Path to the file, absolute or relative to the project." } },
  required: ["path"],
}

function text(s) {
  return { content: [{ type: "text", text: s }], details: {} }
}

export default {
  id: "deja-vu",
  name: "deja-vu",
  description: "Memory from the coding sessions already on this machine",
  register(api) {
    const config = (api && api.pluginConfig) || {}
    const bin = resolveDeja(config.bin)
    const adds = contributions(installerWiring(), config)
    const access = adds.recall ? accessNote(api && api.config, (api && api.id) || "deja-vu") : ""
    let installed = true

    const ask = async (args, input, timeout, cwd = process.cwd()) => {
      const out = await run(bin, args, input, cwd, timeout)
      return out
    }
    const workspace = (ctx) => (ctx && ctx.workspaceDir) || process.cwd()
    let hookExt = null
    const hookCwd = (dir) => (dir && existsSync(dir) ? dir : process.cwd())
    const askExt = async (op, payload, timeout = 20000, cwd = process.cwd()) => {
      if (hookExt === false) return null
      const { env, timedOut } = await runExt(bin, op, payload, hookCwd(cwd), timeout)
      if (env) {
        hookExt = true
        return env
      }
      if (!timedOut) hookExt = false
      return null
    }
    const intOpt = (v) => {
      if (v === undefined || v === null) return undefined
      const n = Math.trunc(Number(v))
      return Number.isFinite(n) ? n : undefined
    }
    const extTool = async (toolName, toolInput, dir, opts = {}) => {
      const resolved = dir || process.cwd()
      if (hookExt === false) return null
      const { env, timedOut } = await runExt(bin, "tool", { tool: toolName, tool_input: toolInput, cwd: resolved, ...opts }, resolved, 120000)
      if (env) {
        hookExt = true
        if (!env.ok) return { ok: false, text: env.error || NOTHING }
        return { ok: true, text: env.output || "" }
      }
      if (!timedOut) hookExt = false
      return null
    }
    // deja recall ranks the project of its working directory first, and the
    // plugin process runs wherever the gateway was started. A tool registered
    // as a factory is built for each run with that run's context, whose
    // workspaceDir is the session's workspace (#5580).
    const answer = (out) => text(out || (installed ? NOTHING : MISSING))

    // --framed: fix and blame print for a person, and what a tool returns goes
    // to the model, which has to get it inside deja's untrusted-data frame.
    // recall frames its own page. /deja below answers the person and stays
    // plain.
    if (adds.tools) {
      api.registerTool(
        (ctx) => ({
          name: "deja_recall",
          description:
            "Search this machine's own past AI coding sessions — every agent used on it, including months before deja was installed. Route file edits to deja_blame and unfamiliar errors to deja_fix; use deja_recall before any plan, review, or recommending an approach, before re-implementing something that may already exist, before the first edit in a task, mid-task whenever concrete names surface (batch alongside reads), before calling a change done or ready to merge, and whenever the user implies the work happened before. Carry recalled constraints into plans and subagent prompts.",
          parameters: RECALL_SCHEMA,
          async execute(_id, params) {
            // The page the MCP recall tool serves, framed and budgeted by deja.
            const dir = workspace(ctx)
            const rawLimit = intOpt(params && params.limit)
            const rawOffset = intOpt(params && params.offset)
            const opts = {}
            if (rawLimit !== undefined) opts.limit = Math.min(20, Math.max(1, rawLimit))
            if (rawOffset !== undefined && rawOffset > 0) opts.offset = Math.min(1e9, rawOffset)
            const ext = await extTool("deja_recall", { query: params && params.query }, dir, opts)
            if (ext !== null) return text(ext.text || NOTHING)
            const call = recallArgv(params)
            const out = await ask(call, undefined, 120000, dir)
            return answer(out || (installed ? recallSilence(call) : ""))
          },
        }),
        { name: "deja_recall" },
      )
      api.registerTool(
        (ctx) => ({
          name: "deja_fix",
          description:
            "Call after an unfamiliar command, test, or CI failure: what this machine ran after that same error before, in sessions where it did not come back. Paste failing output verbatim rather than paraphrasing.",
          parameters: FIX_SCHEMA,
          async execute(_id, params) {
            const dir = workspace(ctx)
            const ext = await extTool("deja_fix", { error: params && params.error }, dir)
            if (ext !== null) return text(ext.text || NOTHING)
            return answer(await ask(["--framed", ...argv("fix", [], params.error)], undefined, 120000, dir))
          },
        }),
        { name: "deja_fix" },
      )
      api.registerTool(
        (ctx) => ({
          name: "deja_blame",
          description:
            "Call before editing, refactoring, or deleting a file: the past sessions that discussed it, so you know why it is shaped the way it is (inline checks into plans and subagent prompts). Session history, not git authorship.",
          parameters: BLAME_SCHEMA,
          async execute(_id, params) {
            const dir = workspace(ctx)
            const ext = await extTool("deja_blame", { path: params && params.path }, dir)
            if (ext !== null) return text(ext.text || NOTHING)
            return answer(await ask(["--framed", ...argv("blame", [], params.path)], undefined, 120000, dir))
          },
        }),
        { name: "deja_blame" },
      )
    }

    // The same three seams `deja install openclaw-auto` writes: the project's
    // digest at the start of the session, recall for each prompt, and the
    // compaction capture. The package lagged the installer on the first and
    // the last, so a gateway with only the package had no digest and lost what
    // a compacted session had been shown.
    if (adds.recall) {
      const promptCache = createSessionStore("openclaw-pkg-cache", { dejaBin: bin })
      const seen = createSeenStore(promptCache)
      const scopeOf = (at) => (at.cwd || "") + "\0" + (at.id || "default")
      const hasBootstrapRecall = (files) => files.some((f) => f && (f.name === "DEJA-RECALL.md" || f.path === "deja://recall"))
      // deja's per-prompt receipt rides under the final reply in a
      // 1:1 chat with the owner on a channel (Telegram, Slack, Discord ...).
      // Not in a group, where other people read the reply, and not on the TUI or
      // the Control UI, which draw the transcript rather than the delivered
      // reply. The session-start notes never go to a channel: hook-context stays
      // --plain, so they are not spent and still come with /deja.
      //
      // By default every DM shares agent:main:main, so a receipt is tied to the
      // run, the conversation and the sender it was asked for, and every new
      // message or prompt in the session drops whatever an earlier turn left.
      const receipts = config.receipt !== false
      const turns = new Map()
      const board = new Map()
      const keep = (map, key, value) => {
        map.delete(key)
        map.set(key, value)
        while (map.size > 256) map.delete(map.keys().next().value)
      }
      // before_dispatch is the hook told whether the message came in a group.
      api.on("before_dispatch", async (event, ctx) => {
        const turn = directTurn(event, ctx)
        if (!turn.key) return
        board.delete(turn.key)
        keep(turns, turn.key, turn)
      })
      // A cron or heartbeat run has no person waiting on its reply.
      const turnChannel = (ctx) => {
        if (ctx && ctx.trigger && ctx.trigger !== "user") return ""
        return (ctx && (ctx.channel || ctx.messageProvider)) || ""
      }
      // Both seams below fire once per agent run, so deja_once is what keeps
      // the digest to the first of them.
      const digest = async (event, ctx) => {
        const at = where(seen, event, ctx, process.cwd())
        const payload = { session_id: at.id, cwd: at.cwd, source: "startup", deja_once: true, deja_prompt_stamps: true }
        const ext = await askExt("context", { ...payload, plain: true, once: true }, 10000, at.cwd)
        if (ext !== null) return ext.ok ? ext.context || "" : ""
        return ask(["hook-context", "--plain"], JSON.stringify(payload), 10000)
      }
      // agent:bootstrap puts the digest in the Project Context. As a plugin
      // hook it is not behind allowConversationAccess, which OpenClaw 2026.8.1+
      // wants for agent_turn_prepare, and it runs under --local too. It is
      // wired only while hooks.internal.enabled is not false.
      let bootstrap = false
      if (typeof api.registerHook === "function" && api.config?.hooks?.internal?.enabled !== false) {
        try {
          api.registerHook(
            "agent:bootstrap",
            async (event) => {
              const context = event && event.context
              if (!context || !Array.isArray(context.bootstrapFiles)) return
              if (hasBootstrapRecall(context.bootstrapFiles)) return
              const at = where(seen, event, context, process.cwd())
              const scope = scopeOf(at)
              const snap = promptCache.read(scope)
              let text
              if (Object.hasOwn(snap.session, "digest")) {
                text = snap.session.digest
              } else {
                if (context.trigger === "heartbeat" || (event && event.trigger === "heartbeat")) return
                text = await digest(event, context)
                promptCache.write(scope, snap.epoch, (s) => {
                  s.digest = text
                })
              }
              if (!text) return
              context.bootstrapFiles.push({ name: "DEJA-RECALL.md", path: "deja://recall", content: text, missing: false })
            },
            { name: "deja-vu-digest", description: "deja's digest of this project's past sessions" },
          )
          bootstrap = true
        } catch {}
      }
      if (!bootstrap) {
        api.on(
          "agent_turn_prepare",
          async (event, ctx) => {
            if (ctx && ctx.trigger === "heartbeat") return
            const at = where(seen, event, ctx, process.cwd())
            const scope = scopeOf(at)
            const snap = promptCache.read(scope)
            if (Object.hasOwn(snap.session.recalled, "__turn_prepare__")) return
            const text = await digest(event, ctx)
            promptCache.write(scope, snap.epoch, (s) => {
              s.recalled["__turn_prepare__"] = text ? "1" : ""
            })
            if (!text) return
            return { prependContext: text }
          },
          { timeoutMs: 15000 },
        )
      }
      api.on(
        "before_prompt_build",
        async (event, ctx) => {
          const prompt = promptText(event)
          if (!prompt) return
          // A heartbeat poll is not a turn anyone asked for, and its stamp hid
          // the session from the user's other sessions (#5270).
          if (ctx && ctx.trigger === "heartbeat") return
          const key = (ctx && ctx.sessionKey) || ""
          // A second prompt hook in the same run keeps the receipt deja already
          // spent on it; one from any other run drops what was left.
          if (key && board.get(key)?.runId !== (ctx && ctx.runId)) board.delete(key)
          const at = where(seen, event, ctx, process.cwd())
          const channel = turnChannel(ctx)
          const turn = key ? turns.get(key) : undefined
          if (key) turns.delete(key)
          const payload = { prompt, session_id: at.id, cwd: at.cwd }
          const wantReceipt = receipts && receiptAllowed(turn, ctx, channel, api.config)
          // One spawn either way. Where the receipt can be shown, deja is asked
          // for its JSON reply: additionalContext, the text --plain prints, goes
          // to the model and the receipt waits for the reply. Anywhere else it
          // stays --plain, and deja keeps the receipt unspent.
          let recall
          const ext = await askExt(
            "prompt",
            wantReceipt ? { ...payload, plain: false, silent: false } : { ...payload, plain: true },
            10000,
            at.cwd,
          )
          if (ext !== null) {
            if (!ext.ok) return
            if (wantReceipt) {
              const line = ext.receipt || receiptLine(ext.note)
              if (line) keep(board, key, { ...turn, runId: ctx.runId, line })
            }
            recall = ext.context || ""
          } else if (wantReceipt) {
            const reply = splitHookReply(await ask(["hook-prompt"], JSON.stringify(payload), 10000))
            const line = receiptLine(reply.notes)
            if (line) keep(board, key, { ...turn, runId: ctx.runId, line })
            recall = reply.context
          } else {
            recall = await ask(["hook-prompt", "--plain"], JSON.stringify(payload), 10000)
          }
          // Silence is the common case — the hook speaks only when the user's
          // own history answers what they just asked.
          if (!recall) return
          return { prependContext: recall }
        },
        { timeoutMs: 15000 },
      )
      // The reply on its way to the chat takes its turn's receipt, as a context
      // block under the answer. The transcript the model reads is written
      // before this, so the line never reaches it.
      if (receipts) {
        api.on("reply_payload_sending", async (event, ctx) => {
          const key = (event && event.sessionKey) || (ctx && ctx.sessionKey) || ""
          const held = key && board.get(key)
          if (!held || !event || event.kind !== "final") return
          // The final reply settles it either way: shown here or dropped.
          board.delete(key)
          if (!replyTakes(held, event, ctx)) return
          return { payload: withReceipt(event.payload, held.line) }
        })
      }
      // Compaction throws away the blocks this session was shown, and the list
      // that stops them repeating outlives it. The event names the session
      // file, which still holds the turns about to be summarised: deja reads
      // them from it, and the next prompt's recall carries the packet.
      api.on(
        "before_compaction",
        async (event, ctx) => {
          const at = where(seen, event, ctx, process.cwd())
          const payload = {
            session_id: at.id,
            transcript_path: (event && event.sessionFile) || "",
            cwd: at.cwd,
            harness: "openclaw",
          }
          const ext = await askExt("precompact", { ...payload, source: "openclaw" }, 10000, at.cwd)
          if (ext === null) {
            await ask(["hook-precompact"], JSON.stringify(payload), 10000)
          }
          promptCache.compact(scopeOf(at), false)
        },
        { timeoutMs: 15000 },
      )
      // The session is over: its live stamp goes, so the next session's MCP
      // recall can answer with it now rather than twenty minutes from now.
      // session_end names the transcript id the hooks above stamped.
      api.on(
        "session_end",
        async (event, ctx) => {
          const id = (event && event.sessionId) || where(seen, event, ctx, process.cwd()).id
          if (!id) return
          // Read, not where(): the end must not file its id under the key.
          const last = (ctx && ctx.sessionKey && seen.get(ctx.sessionKey)) || {}
          const cwd = (ctx && ctx.workspaceDir) || last.dir || process.cwd()
          const ext = await askExt("session-end", { session_id: id, cwd }, 5000, cwd)
          if (ext === null) {
            await ask(["hook-session-end"], JSON.stringify({ session_id: id, cwd }), 5000)
          }
        },
        { timeoutMs: 10000 },
      )
      // A tool result middleware rewrites what the model reads back from a
      // tool, the one place a line arrives beside the result it is about. It
      // needs contracts.agentToolResultMiddleware in the manifest; an OpenClaw
      // without the API goes without it.
      if (typeof api.registerAgentToolResultMiddleware === "function") {
        api.registerAgentToolResultMiddleware(
          async (event, ctx) => {
            try {
              if (ctx && ctx.trigger === "heartbeat") return
              const call = toolCall(event, where(seen, event, ctx, process.cwd()))
              if (!call) return
              const op = call.args[0] === "hook-tool-after" ? "tool-after" : "tool-before"
              const ext = await askExt(op, { ...call.payload, tool: call.payload.tool_name, plain: true }, 5000, call.payload.cwd)
              const line = ext !== null ? (ext.ok ? ext.context || "" : "") : await ask(call.args, JSON.stringify(call.payload), 5000)
              if (!line) return
              const content = Array.isArray(event.result && event.result.content) ? event.result.content : []
              return { result: { ...event.result, content: [...content, { type: "text", text: line }] } }
            } catch {}
          },
          { runtimes: ["openclaw"] },
        )
      }
    }

    // /deja answers the person directly. On the TUI and the Control UI the
    // hooks above only reach the model, so this reply is also where deja's own
    // notes reach them there.
    try {
      if (typeof api.registerCommand === "function") {
        api.registerCommand({
          name: "deja",
          description: "Search this machine's past AI coding sessions",
          acceptsArgs: true,
          async handler(ctx) {
            const query = String((ctx && ctx.args) || "").trim()
            if (!query) return { text: "Say what to look for: /deja <error, file, or decision>" }
            let out
            const extSearch = await askExt("slash", { args: query }, 120000)
            if (extSearch !== null) {
              if (!extSearch.ok) return { text: extSearch.error || NOTHING }
              out = extSearch.output || ""
            } else {
              out = await ask(argv("search", [], query), undefined, 120000)
              // Asked here rather than read from installed: the startup check may
              // not have answered yet, and an empty search reads as no history.
              if (!out && !(await run(bin, ["--version"]))) return { text: MISSING }
            }
            const extNotes = await askExt("context", { notes_only: true }, 10000)
            const rawNotes = extNotes !== null ? (extNotes.ok ? extNotes.note || extNotes.output || "" : "") : await ask(["hook-context", "--notes"], undefined, 10000)
            const notes = [rawNotes, access].filter(Boolean).join("\n")
            return { text: notes ? (out || NOTHING) + "\n\n" + notes : out || NOTHING }
          },
        })
      }
    } catch {}

    // So is a missing grant: the gateway's own warning names the key but not
    // what it costs.
    if (access) {
      try {
        api.logger && api.logger.warn && api.logger.warn("deja-vu: " + access)
      } catch {}
    }

    // A missing binary is reported once, through the host, not on every turn.
    run(bin, ["--version"]).then((v) => {
      if (v) return
      installed = false
      try {
        api.logger && api.logger.warn && api.logger.warn("deja-vu: " + MISSING)
      } catch {}
    })
  },
}
