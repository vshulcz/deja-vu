import { chmodSync, existsSync, mkdirSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs"
import { homedir, tmpdir } from "node:os"
import { dirname, isAbsolute, join } from "node:path"

// The pure half of the plugin: everything that reads deja's output or
// opencode's message shape, with no process and no host. Kept apart from
// index.js because opencode loads every function a plugin module exports —
// a helper exported next to the plugin gets called as one.

// contextText pulls the recall out of whatever hook-context printed. deja
// answers in the Claude Code hook shape; older builds and `--plain` print bare
// text, so both are accepted.
export function contextText(raw) {
  const text = String(raw || "").trim()
  if (!text) return { context: "", receipt: "" }
  try {
    const parsed = JSON.parse(text)
    return {
      context: parsed?.hookSpecificOutput?.additionalContext || "",
      receipt: parsed?.systemMessage || "",
    }
  } catch {
    return { context: text, receipt: "" }
  }
}

// userTurns is every user message in a request, oldest first, each with a key
// that stays the same from one request to the next: opencode's message id, or
// where a shape carries none, the message's place among the user messages and
// its text.
//
// The prompt leaves out the recall a plugin already appended. A host that keeps
// its request messages from one model call to the next hands the block back,
// and read as part of the question it changed the key on every call: recall was
// asked again each step and each answer appended, six blocks on one message.
export function userTurns(messages) {
  const list = Array.isArray(messages) ? messages : []
  const out = []
  for (const m of list) {
    if (m?.info?.role !== "user") continue
    const sessionID = m.info?.sessionID || ""
    const parts = (m.parts || []).filter((p) => p?.type === "text" && p.text)
    const texts = parts.map((p, i) => (i === parts.length - 1 ? trailingRecall(p.text).body : p.text))
    const prompt = texts.join("\n").trim()
    out.push({ key: m.info?.id || sessionID + "#" + out.length + "#" + prompt, parts, prompt, sessionID })
  }
  return out
}

const RECALL_OPEN = "<deja-recall>"
const RECALL_CLOSE = "</deja-recall>"

// trailingRecall splits the recall blocks appended to the end of a text off
// it: what the user wrote, and the blocks in the order they were added. extra
// is a block as this plugin was given it, so one that came unframed is found
// too.
export function trailingRecall(text, extra = "") {
  const raw = String(text || "")
  let body = raw
  const blocks = []
  for (;;) {
    const t = body.trimEnd()
    if (extra && t.endsWith(extra) && t.length > extra.length) {
      blocks.unshift(extra)
      body = t.slice(0, t.length - extra.length)
      continue
    }
    const at = t.endsWith(RECALL_CLOSE) ? t.lastIndexOf(RECALL_OPEN) : -1
    if (at <= 0) break
    blocks.unshift(t.slice(at))
    body = t.slice(0, at)
  }
  return blocks.length ? { body: body.trimEnd(), blocks } : { body: raw, blocks }
}

// settleRecall leaves the message's last text part carrying one recall block:
// the one already there if another copy of the plugin put it in first, or
// extra. Run on every model call, it replaces rather than appends, so a host
// that keeps the message between calls does not collect a block per call.
export function settleRecall(tail, extra) {
  if (!tail || typeof tail.text !== "string") return
  const { body, blocks } = trailingRecall(tail.text, extra)
  const keep = blocks[0] || extra || ""
  const next = keep ? body + "\n\n" + keep : body
  if (next !== tail.text) tail.text = next
}

// cliPluginPath is where `deja install opencode-auto` writes its own plugin.
// opencode loads that file and this package side by side — both are entries in
// the resolved `plugin` list — and both push recall onto the system prompt, so
// whoever finds the other has to stand down. Mirrors opencodeConfigHome() in
// the installer: XDG_CONFIG_HOME wins, else ~/.config.
export function cliPluginPath(env, home) {
  const base = (env && env.XDG_CONFIG_HOME) || join(home || "", ".config")
  return join(base, "opencode", "plugins", "deja.js")
}

// configPaths are the two names opencode accepts for the global config, in the
// order `deja install opencode` looks for them.
export function configPaths(env, home) {
  const base = (env && env.XDG_CONFIG_HOME) || join(home || "", ".config")
  return [join(base, "opencode", "opencode.json"), join(base, "opencode", "opencode.jsonc")]
}

// mcpWired reports whether that config already runs deja as an MCP server,
// which is what `deja install opencode` writes. The tools this package
// registers do the same six things, so when the server is there they are a
// second copy of it in the model's tool list.
//
// The file is JSONC — opencode ships it with comments — so comments come out
// before the parse. A config this cannot read is treated as not wired: losing
// the tools on a file we misread is worse than listing them twice.
export function mcpWired(text) {
  try {
    const config = JSON.parse(stripJSONComments(String(text || "")))
    // 1.x keeps servers directly under `mcp`, 2.x under `mcp.servers`.
    const mcp = (config && config.mcp) || {}
    const entry = mcp.deja || (mcp.servers && mcp.servers.deja)
    // A server kept in the file but switched off offers nothing, so the
    // tools here are not a second copy of it. 1.x switches it off with
    // `enabled: false`, 2.x with `disabled: true`.
    return Boolean(entry) && entry.enabled !== false && entry.disabled !== true
  } catch {
    return false
  }
}

// stripJSONComments removes // and /* */ outside strings. Small on purpose: it
// only has to survive the file our own installer writes.
export function stripJSONComments(text) {
  let out = ""
  let inString = false
  for (let i = 0; i < text.length; i++) {
    const c = text[i]
    if (inString) {
      out += c
      if (c === "\\") {
        out += text[++i] || ""
      } else if (c === '"') {
        inString = false
      }
      continue
    }
    if (c === '"') {
      inString = true
      out += c
      continue
    }
    if (c === "/" && text[i + 1] === "/") {
      while (i < text.length && text[i] !== "\n") i++
      out += "\n"
      continue
    }
    if (c === "/" && text[i + 1] === "*") {
      i += 2
      while (i + 1 < text.length && !(text[i] === "*" && text[i + 1] === "/")) i++
      i++
      continue
    }
    out += c
  }
  return out
}

// contributions decides what this package adds, given what `deja install`
// already wired and what the user turned off in their config. It is the whole
// rule in one place: fill the gaps, never repeat the installer.
export function contributions(wiring, config = {}) {
  const wired = wiring || {}
  return {
    tools: config.tools !== false && !wired.mcp,
    recall: config.autoRecall !== false && !wired.recall,
  }
}

// argv builds the call for a query somebody typed, rather than handing the text
// to deja as it stands.
//
// A query that starts with a dash is read as a flag: `deja search --no-verify`
// exits on an unknown flag, run() turns a failed call into "", and the model is
// told the history holds nothing about --no-verify while the sessions that
// discuss it sit right there. A wrong answer, where an error would at least be
// visible. `--` ends the flags.
//
// Sent only when the query needs it, so a deja too old to know the terminator
// on this subcommand still answers every ordinary query.
export function argv(cmd, flags, text) {
  const arg = String(text)
  return arg.startsWith("-") ? [cmd, ...flags, "--", arg] : [cmd, ...flags, arg]
}

// recallArgv is the call behind the deja_recall tool: `deja recall`, the page
// the MCP recall tool serves, framed and held to its 4 KB budget by deja
// itself. The tool used to run `search --json`, which carries every hit's
// matched messages whole and handed a model 661,528 characters for five hits
// (#5568). The page ends with "call recall again with offset=N"; offset is
// how the tool asks for that, and it is sent only past the first page, so a
// deja too old to know --offset still answers the first one.
export function recallArgv(args) {
  const count = (v, fallback) => {
    const n = Math.trunc(Number(v))
    return v == null || v === "" || !Number.isFinite(n) ? fallback : n
  }
  const limit = clampLimit(count(args && args.limit, 5))
  const offset = Math.min(1e9, Math.max(0, count(args && args.offset, 0)))
  const flags = ["--limit", String(limit)]
  if (offset > 0) flags.push("--offset", String(offset))
  return argv("recall", flags, args && args.query)
}

// recallSilence is the answer when `deja recall` printed nothing. Past the
// first page that is a deja older than --offset refusing the flag, not an
// empty history: recall always prints a line, "No more matches" included.
export function recallSilence(call) {
  if (call.includes("--offset")) {
    return "This deja cannot page recall. Run deja update, then ask again with the same offset."
  }
  return "Nothing in this machine's history matches that."
}

// clampLimit keeps a model that asks for a hundred sessions from spending the
// window on a tail nobody reads.
export function clampLimit(value, fallback = 5) {
  const asked = Number(value)
  if (!Number.isFinite(asked)) return fallback
  return Math.min(20, Math.max(1, Math.trunc(asked)))
}

// TOOL_SPECS is where the plugin leaves its tool list on the hooks it returns,
// for the 2.x entry to register. A symbol, so 1.x never reads it as a hook.
export const TOOL_SPECS = Symbol.for("opencode-deja.tools")

// zodTools builds the 1.x tool table from the specs, through opencode's own
// helper and the zod it re-exports.
export function zodTools(tool, specs) {
  const schema = tool.schema
  const out = {}
  for (const [name, spec] of Object.entries(specs)) {
    const args = {}
    for (const [key, arg] of Object.entries(spec.args)) {
      let field = arg.type === "number" ? schema.number() : schema.string()
      if (arg.optional) field = field.optional()
      args[key] = field.describe(arg.description)
    }
    out[name] = tool({ description: spec.description, args, execute: spec.execute })
  }
  return out
}

// jsonSchema is a spec's arguments in the form 2.x takes a tool's input.
export function jsonSchema(args) {
  const properties = {}
  const required = []
  for (const [key, arg] of Object.entries(args)) {
    properties[key] = { type: arg.type, description: arg.description }
    if (!arg.optional) required.push(key)
  }
  return { type: "object", properties, required }
}

// turnEnded is the session whose turn a 2.x event says is over, or "". 2.0.22
// ends a turn with session.execution.succeeded, .failed or .interrupted and
// never publishes 1.x's session.idle, which its schema still carries (#4571).
export function turnEnded(event) {
  switch (event?.type) {
    case "session.execution.succeeded":
    case "session.execution.failed":
    case "session.execution.interrupted":
    case "session.idle":
      return event.data?.sessionID || ""
  }
  return ""
}

// v1Messages shows 2.x's request messages in the 1.x shape userTurns reads.
// The parts are 2.x's own content objects, so text appended to one lands in
// the request.
export function v1Messages(messages, sessionID) {
  const list = Array.isArray(messages) ? messages : []
  return list.map((m) => ({
    info: { id: m?.id, role: m?.role, sessionID: sessionID || "" },
    parts: Array.isArray(m?.content) ? m.content : [],
  }))
}

// resultText is the text a finished 2.x tool call returned: `content` is a
// string or a list of blocks, and a tool may leave only `output`.
export function resultText(result) {
  if (typeof result === "string") return result
  const r = result || {}
  if (typeof r.content === "string") return r.content
  if (Array.isArray(r.content)) {
    return r.content
      .filter((c) => c?.type === "text")
      .map((c) => c.text)
      .join("\n")
  }
  return typeof r.output === "string" ? r.output : ""
}

// v1Client is the slice of the 1.x client the plugin reads, over 2.x's ctx. A
// 2.x server plugin has no channel to the TUI, so there is no tui here, and the
// plugin asks deja to keep the person's lines rather than spend them. The
// session lookup is what names a sub-agent's parent: without it the
// sub-agent's digest and recall led with the parent that spawned it (#4548).
// 1.x answers through client.session.get, 2.x through ctx.session.get, whose
// SessionInfo carries parentID.
export function v1Client(ctx) {
  return {
    session: {
      get: async ({ path } = {}) => {
        if (!path?.id || typeof ctx?.session?.get !== "function") return { data: {} }
        return { data: (await ctx.session.get({ sessionID: path.id })) || {} }
      },
    },
  }
}

// v1ToolName is the 1.x name for a 2.x tool, where the two differ in a way the
// hooks look at: the spawn tool and the shell.
export function v1ToolName(name) {
  if (name === "subagent") return "task"
  if (name === "shell") return "bash"
  return name
}

// promptCachePath resolves the on-disk state file so plugin reloads and process
// restarts re-emit the exact prompt-prefix blocks instead of busting cache.
function isTempDejaBin(dejaBin) {
  if (!dejaBin) return false
  const fold = (s) => {
    const clean = s.replace(/\\/g, "/").replace(/\/+$/, "")
    return process.platform === "win32" ? clean.toLowerCase() : clean
  }
  const bin = fold(dejaBin)
  return [tmpdir(), "/tmp", "/private/tmp", "/var/folders"]
    .filter(Boolean)
    .some((base) => {
      const root = fold(base)
      return root && bin.startsWith(root + "/")
    })
}

export function promptCachePath(name, dejaBin = "") {
  const override = process.env.DEJA_PROMPT_CACHE_FILE
  if (override) return override
  if (isTempDejaBin(dejaBin)) {
    return join(dirname(dejaBin), ".deja-cache", `${name}.json`)
  }
  const xdg = process.env.XDG_CACHE_HOME
  if (xdg && isAbsolute(xdg)) return join(xdg, "deja-vu", `${name}.json`)
  const base = process.platform === "win32"
    ? (process.env.LOCALAPPDATA || join(homedir(), "AppData", "Local"))
    : join(homedir(), ".cache")
  return join(base, "deja-vu", `${name}.json`)
}

function nullMap(src) {
  const out = Object.create(null)
  if (!src || typeof src !== "object" || Array.isArray(src)) return out
  for (const [k, v] of Object.entries(src)) out[k] = v
  return out
}

function readCacheFile(file) {
  try {
    if (!existsSync(file)) return { version: 1, sessions: Object.create(null) }
    const parsed = JSON.parse(readFileSync(file, "utf8"))
    if (parsed && parsed.version === 1 && parsed.sessions && typeof parsed.sessions === "object" && !Array.isArray(parsed.sessions)) {
      return { version: 1, sessions: nullMap(parsed.sessions) }
    }
  } catch {}
  return { version: 1, sessions: Object.create(null) }
}

function writeCacheFile(file, data, keepScope, maxSessions) {
  try {
    const entries = Object.entries(data.sessions)
    if (entries.length > maxSessions) {
      entries.sort((a, b) => (b[1]?.updatedAt || 0) - (a[1]?.updatedAt || 0))
      const trimmed = Object.create(null)
      if (keepScope && Object.hasOwn(data.sessions, keepScope) && data.sessions[keepScope]) {
        trimmed[keepScope] = data.sessions[keepScope]
      }
      for (const [k, v] of entries) {
        if (Object.keys(trimmed).length >= maxSessions) break
        trimmed[k] = v
      }
      data.sessions = trimmed
    }
    const dir = dirname(file)
    mkdirSync(dir, { recursive: true, mode: 0o700 })
    if (
      process.platform !== "win32" &&
      dir !== "/" &&
      dir !== "/tmp" &&
      dir !== "/private/tmp" &&
      dir !== tmpdir().replace(/\/+$/, "") &&
      dir !== homedir().replace(/\/+$/, "")
    ) {
      try { chmodSync(dir, 0o700) } catch {}
    }
    const tmp = `${file}.${process.pid}.${Math.random().toString(36).slice(2)}.tmp`
    try {
      writeFileSync(tmp, JSON.stringify(data), { encoding: "utf8", mode: 0o600 })
      if (process.platform !== "win32") chmodSync(tmp, 0o600)
      renameSync(tmp, file)
    } catch (err) {
      try { rmSync(tmp, { force: true }) } catch {}
      throw err
    }
  } catch {}
}

function cloneSession(raw) {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return { epoch: 0, updatedAt: 0, recalled: Object.create(null) }
  }
  const out = {
    epoch: Number.isInteger(raw.epoch) ? raw.epoch : 0,
    updatedAt: typeof raw.updatedAt === "number" ? raw.updatedAt : 0,
    recalled: nullMap(raw.recalled),
  }
  if (Object.hasOwn(raw, "digest")) out.digest = String(raw.digest ?? "")
  if (raw.told) out.told = true
  if (typeof raw.empties === "number") out.empties = raw.empties
  return out
}

export function rememberEntry(mapObj, key, val, maxEntries = 1000) {
  if (Object.hasOwn(mapObj, key)) delete mapObj[key]
  mapObj[key] = val
  const keys = Object.keys(mapObj)
  while (keys.length > maxEntries) {
    delete mapObj[keys.shift()]
  }
}

// createSessionStore persists per-session digest and recall maps with a
// monotonic compaction epoch so a slower worker on an old epoch cannot
// overwrite post-compaction state.
export function createSessionStore(name, { maxSessions = 256, dejaBin = "" } = {}) {
  const path = () => promptCachePath(name, dejaBin)
  const read = (scope) => {
    const data = readCacheFile(path())
    const raw = Object.hasOwn(data.sessions, scope) ? data.sessions[scope] : undefined
    const session = cloneSession(raw)
    return { epoch: session.epoch, session }
  }
  const write = (scope, expectedEpoch, mutator) => {
    const file = path()
    const data = readCacheFile(file)
    const raw = Object.hasOwn(data.sessions, scope) ? data.sessions[scope] : undefined
    const cur = cloneSession(raw)
    if (cur.epoch > expectedEpoch) return { stale: true, epoch: cur.epoch, session: cur }
    mutator(cur)
    cur.epoch = cur.epoch > expectedEpoch ? cur.epoch : expectedEpoch
    cur.updatedAt = Date.now()
    data.sessions[scope] = cur
    writeCacheFile(file, data, scope, maxSessions)
    return { stale: false, epoch: cur.epoch, session: cur }
  }
  const compact = (scope, { clearDigest = false } = {}) => {
    const file = path()
    const data = readCacheFile(file)
    const raw = Object.hasOwn(data.sessions, scope) ? data.sessions[scope] : undefined
    const cur = cloneSession(raw)
    const next = { epoch: cur.epoch + 1, updatedAt: Date.now(), recalled: Object.create(null) }
    if (!clearDigest && Object.hasOwn(cur, "digest")) next.digest = cur.digest
    if (cur.told) next.told = true
    data.sessions[scope] = next
    writeCacheFile(file, data, scope, maxSessions)
    return next
  }
  return { read, write, compact }
}
