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
  sessionKey,
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
    if (input !== undefined) child.child.stdin.end(input)
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
          "Search this machine's own past AI coding sessions — every agent used on it, including months before deja was installed. Use before debugging an error or re-implementing something that may already exist, and whenever the user implies the work happened before.",
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

    if (adds.recall) {
      api.on(
        "before_prompt_build",
        async (event, ctx) => {
          const prompt = promptText(event)
          if (!prompt) return
          const key = sessionKey(event, ctx)
          const recall = await ask(
            ["hook-prompt", "--plain"],
            JSON.stringify({ prompt, session_id: key, cwd: process.cwd() }),
            10000,
          )
          // Silence is the common case — the hook speaks only when the user's
          // own history answers what they just asked.
          if (!recall) return
          return { prependContext: recall }
        },
        { timeoutMs: 15000 },
      )
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
