// deja-vu for OpenClaw: recall from the coding sessions already on this
// machine before each turn, and three tools to ask the history directly.
import { execFile } from "node:child_process"
import { accessSync, constants, existsSync, readFileSync } from "node:fs"
import { createRequire } from "node:module"
import { homedir } from "node:os"
import { join } from "node:path"
import { promisify } from "node:util"

import {
  argv,
  configPath,
  contributions,
  installerPluginPath,
  mcpWired,
  promptText,
  toolCall,
  where,
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
    query: { type: "string", description: "Error text, identifier, command, or a short description of the problem. Several words are ANDed." },
    limit: { type: "integer", description: "How many sessions to return. Default 5." },
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
    let installed = true

    const ask = async (args, input, timeout) => {
      const out = await run(bin, args, input, process.cwd(), timeout)
      return out
    }
    const answer = (out) => text(out || (installed ? NOTHING : MISSING))

    if (adds.tools) {
      api.registerTool({
        name: "deja_recall",
        description:
          "Search this machine's own past AI coding sessions — every agent used on it, including months before deja was installed. Use before debugging an error or re-implementing something that may already exist, before the first edit in a task and before calling a change done or ready to merge, and whenever the user implies the work happened before.",
        parameters: RECALL_SCHEMA,
        async execute(_id, params) {
          const limit = String(Math.min(Math.max(Number(params.limit) || 5, 1), 20))
          return answer(await ask(argv("search", ["--limit", limit], params.query), undefined, 120000))
        },
      })
      api.registerTool({
        name: "deja_fix",
        description:
          "What this machine ran after that same error before, in the sessions where the error did not come back. Paste the failing output verbatim rather than a paraphrase.",
        parameters: FIX_SCHEMA,
        async execute(_id, params) {
          return answer(await ask(argv("fix", [], params.error), undefined, 120000))
        },
      })
      api.registerTool({
        name: "deja_blame",
        description:
          "The past sessions that discussed a file, so you know why it is shaped the way it is before editing, refactoring or deleting it. Session history, not git authorship.",
        parameters: BLAME_SCHEMA,
        async execute(_id, params) {
          return answer(await ask(argv("blame", [], params.path), undefined, 120000))
        },
      })
    }

    // The same three seams `deja install openclaw-auto` writes: the project's
    // digest at the start of the session, recall for each prompt, and the
    // compaction capture. The package lagged the installer on the first and
    // the last, so a gateway with only the package had no digest and lost what
    // a compacted session had been shown.
    if (adds.recall) {
      const seen = new Map()
      // Both seams below fire once per agent run, so deja_once is what keeps
      // the digest to the first of them.
      const digest = (event, ctx) => {
        const at = where(seen, event, ctx, process.cwd())
        return ask(
          ["hook-context", "--plain"],
          JSON.stringify({ session_id: at.id, cwd: at.cwd, source: "startup", deja_once: true }),
          10000,
        )
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
              const text = await digest(event, context)
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
            const text = await digest(event, ctx)
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
          const at = where(seen, event, ctx, process.cwd())
          const recall = await ask(
            ["hook-prompt", "--plain"],
            JSON.stringify({ prompt, session_id: at.id, cwd: at.cwd }),
            10000,
          )
          // Silence is the common case — the hook speaks only when the user's
          // own history answers what they just asked.
          if (!recall) return
          return { prependContext: recall }
        },
        { timeoutMs: 15000 },
      )
      // Compaction throws away the blocks this session was shown, and the list
      // that stops them repeating outlives it. The event names the session
      // file, which still holds the turns about to be summarised: deja reads
      // them from it, and the next prompt's recall carries the packet.
      api.on(
        "before_compaction",
        async (event, ctx) => {
          const at = where(seen, event, ctx, process.cwd())
          await ask(
            ["hook-precompact"],
            JSON.stringify({
              session_id: at.id,
              transcript_path: (event && event.sessionFile) || "",
              cwd: at.cwd,
              harness: "openclaw",
            }),
            10000,
          )
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
          await ask(["hook-session-end"], JSON.stringify({ session_id: id }), 5000)
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
              const call = toolCall(event, where(seen, event, ctx, process.cwd()))
              if (!call) return
              const line = await ask(call.args, JSON.stringify(call.payload), 5000)
              if (!line) return
              const content = Array.isArray(event.result && event.result.content) ? event.result.content : []
              return { result: { ...event.result, content: [...content, { type: "text", text: line }] } }
            } catch {}
          },
          { runtimes: ["openclaw"] },
        )
      }
    }

    // /deja answers the person directly. The hooks above only reach the
    // model, so this reply is also where deja's own notes reach them.
    try {
      if (typeof api.registerCommand === "function") {
        api.registerCommand({
          name: "deja",
          description: "Search this machine's past AI coding sessions",
          acceptsArgs: true,
          async handler(ctx) {
            const query = String((ctx && ctx.args) || "").trim()
            if (!query) return { text: "Say what to look for: /deja <error, file, or decision>" }
            const out = await ask(argv("search", [], query), undefined, 120000)
            // Asked here rather than read from installed: the startup check may
            // not have answered yet, and an empty search reads as no history.
            if (!out && !(await run(bin, ["--version"]))) return { text: MISSING }
            const notes = await ask(["hook-context", "--notes"], undefined, 10000)
            return { text: notes ? (out || NOTHING) + "\n\n" + notes : out || NOTHING }
          },
        })
      }
    } catch {}

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
