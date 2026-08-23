// opencode-deja — the sessions you already have, inside opencode.
//
// opencode remembers its own sessions. This plugin answers the other question:
// what you did in Claude Code, Codex, Cursor, Gemini and sixteen more agents on
// this machine, including months before deja existed. The index is deja's; this
// file is the seam: six tools the model can call, plus recall that arrives
// without being asked for.

import { createRequire } from "node:module"
import { execFile } from "node:child_process"
import { promisify } from "node:util"
import { homedir } from "node:os"
import { join } from "node:path"
import { accessSync, constants, existsSync, readFileSync } from "node:fs"

import {
  clampLimit,
  cliPluginPath,
  configPaths,
  contextText,
  contributions,
  lastUserText,
  mcpWired,
} from "./lib.js"

const require = createRequire(import.meta.url)
const run_ = promisify(execFile)

const WINDOWS = process.platform === "win32"
const PLATFORM = WINDOWS ? "windows" : process.platform
const ARCH = process.arch === "x64" ? "amd64" : process.arch
const EXE = WINDOWS ? "deja.exe" : "deja"

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

  const hooks = {}

  // What `deja install` already wired for opencode is not repeated here. It
  // writes an MCP server (the same six answers, under their own names) and,
  // with --auto, a plugin file of its own that opencode loads beside this
  // package — verified with `opencode debug config`, which resolves both into
  // one plugin list. Whatever the installer wrote wins, because that is the
  // copy `deja install` keeps current; this package fills the gaps.
  const adds = contributions(installerWiring(), config)

  if (adds.tools && tool) {
    const schema = tool.schema
    hooks.tool = {
      deja_recall: tool({
        description:
          "Search this machine's own past AI coding sessions — every agent used on it, including months before deja was installed. Use before debugging an error or re-implementing anything that may already exist. Match on the most specific token available: an exact error string, function name, file path or flag.",
        args: {
          query: schema.string().describe("Specific tokens to match. Several words are ANDed."),
          limit: schema.number().optional().describe("How many sessions to return. Default 5."),
        },
        async execute(args) {
          const limit = String(clampLimit(args.limit))
          return answer(await ask(["search", "--json", "--limit", limit, String(args.query)]))
        },
      }),
      deja_session: tool({
        description:
          "A full digest of the single best-matching past session — what was tried, what was decided, what it cost. Use after deja_recall when the reasoning behind an earlier decision matters, not just that it happened.",
        args: {
          query: schema.string().describe("A query, or a session id prefix returned by deja_recall."),
        },
        async execute(args) {
          return answer(await ask(["ctx", String(args.query)]))
        },
      }),
      deja_blame: tool({
        description:
          "The past sessions that discussed a file, so you know why it is shaped the way it is before editing, refactoring or deleting it. Session history, not git authorship.",
        args: {
          path: schema.string().describe("Path to the file, absolute or relative to the project."),
        },
        async execute(args) {
          return answer(await ask(["blame", String(args.path), "--json"]))
        },
      }),
      deja_fix: tool({
        description:
          "What this machine ran after that same error before, in the sessions where the error did not come back. Paste the failing output verbatim rather than a paraphrase — the match is on the error's own words.",
        args: {
          error: schema.string().describe("The failing output, copied as it was printed."),
        },
        async execute(args) {
          return answer(await ask(["fix", String(args.error)]))
        },
      }),
      deja_how: tool({
        description:
          "The real invocation this machine uses for a build, test, deploy or script, with the flags it actually ran, ordered by how many sessions ran it. A guessed command is plausible and fails on this setup.",
        args: {
          what: schema.string().describe("The thing to run: a tool, a task, a script name."),
        },
        async execute(args) {
          return answer(await ask(["how", String(args.what)]))
        },
      }),
      deja_remember: tool({
        description:
          "Store one durable decision once it is settled, as a single self-contained fact that will make sense months later. Not transcripts, not a summary of the conversation, and not anything already obvious from the code.",
        args: {
          text: schema.string().describe("The decision, in one or two sentences, with the reason it was taken."),
        },
        async execute(args) {
          const written = await ask(["remember", String(args.text)])
          if (!installed) return MISSING
          return written || "deja did not record that."
        },
      }),
    }
  }

  if (!adds.recall) return hooks

  // opencode has no session-start hook. The system prompt is assembled on
  // every request, so the session digest is fetched once and pushed there —
  // the same place Claude Code's SessionStart output lands.
  hooks["experimental.chat.system.transform"] = async (input, output) => {
    try {
      const key = input?.sessionID || "default"
      if (!digests.has(key)) {
        const { context, receipt } = contextText(await ask(["hook-context"], undefined, 30000))
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
        output.system.push(context)
        return
      }
      // Nothing recalled: either this machine has no history yet, or the first
      // index is still building. Only the second is worth saying, and saying it
      // drops the empty answer so the next turn asks again.
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
  hooks["experimental.chat.messages.transform"] = async (_input, output) => {
    try {
      const { parts, prompt } = lastUserText(output?.messages)
      if (!prompt) return
      const raw = await ask(["hook-prompt"], JSON.stringify({ prompt, cwd }))
      if (!raw) return
      const extra = JSON.parse(raw)?.hookSpecificOutput?.additionalContext
      if (!extra) return
      parts[parts.length - 1].text += "\n\n" + extra
    } catch {
      // memory is optional: never break the session over it
    }
  }

  // Compaction is about to throw the working transcript away. Index what
  // exists now, so the session survives in memory after the window collapses.
  hooks["experimental.session.compacting"] = async () => {
    try {
      await ask(["hook-precompact"], undefined, 60000)
    } catch {
      // memory is optional: never break a compaction over it
    }
  }

  return hooks
}
