import { chmodSync, existsSync, mkdirSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs"
import { homedir, tmpdir } from "node:os"
import { dirname, isAbsolute, join } from "node:path"

// The pure half of the extension: everything that reads deja's output or
// decides what this package adds, with no process and no host, so it can be
// tested without pi.

// installerExtensionPath is where `deja install pi-auto` writes its own
// extension. pi loads that file and this package side by side, and both
// would inject recall and register /deja, so this package stands down when
// the installer's copy is present. Mirrors PiConfigDir() in the installer:
// ~/.pi/agent, or PI_CODING_AGENT_DIR when pi was told to live elsewhere.
export function installerExtensionPath(home, env = {}) {
  let dir = String(env.PI_CODING_AGENT_DIR || "")
  if (dir === "~") dir = home || ""
  else if (dir.startsWith("~/") || dir.startsWith("~\\")) dir = join(home || "", dir.slice(2))
  return join(dir || join(home || "", ".pi", "agent"), "extensions", "deja.ts")
}

// installerExtensionPaths are every place `deja install` writes an extension
// this package would duplicate. omp runs the same package and the installer
// writes it under ~/.omp/agent/extensions/deja/index.js, so checking pi's path
// alone left both running side by side (#3180).
export function installerExtensionPaths(home, env = {}) {
  return [
    installerExtensionPath(home, env),
    join(home || "", ".omp", "agent", "extensions", "deja", "index.js"),
  ]
}

// contextText pulls the recall out of whatever hook-context printed. deja
// answers in the Claude Code hook shape; older builds and `--plain` print
// bare text, so both are accepted.
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

// argv builds the call for a query somebody typed. A query that starts with
// a dash is read by deja as a flag and the call fails, which run() turns into
// "nothing in the history" — a wrong answer where an error would at least be
// visible. `--` ends the flags; sent only when the query needs it.
export function argv(cmd, flags, text) {
  const arg = String(text)
  return arg.startsWith("-") ? [cmd, ...flags, "--", arg] : [cmd, ...flags, arg]
}

// sessionKey is what per-prompt recall dedups on: a hit shown once in a
// session is not shown again. Without one it repeats itself every message —
// which it did, because pi keeps the id on ctx.sessionManager and nowhere the
// event or ctx.session shapes below look (#3179). The manager first, the same
// order as the extension `deja install pi-auto` writes; the rest for hosts
// that put an id on the event.
export function sessionKey(event, ctx) {
  try {
    const m = ctx && ctx.sessionManager
    const id = m && (typeof m.getSessionId === "function" ? m.getSessionId() : m.sessionId)
    if (id) return String(id)
  } catch {}
  return (event && (event.sessionId || event.session_id)) || (ctx && ctx.session && ctx.session.id) || ""
}

// branchHoldsRecall reports whether what pi sends the model from this branch
// holds a recall block this package injected: what follows the last
// compaction, and the entries it kept from before it, firstKeptEntryId on.
// pi keeps the block as a custom_message and sends it again on a resume, and
// a fork copies it from its source.
//
// What counts is the start digest, not any recall: a prompt's recall goes
// under the same customType, and a compaction that kept a later one but not
// the digest left a resumed session with no digest (#5314). The digest
// carries details.digest; a branch with no marked digest anywhere was written
// before the mark, and any block of ours still counts there.
export function branchHoldsRecall(branch) {
  const entries = Array.isArray(branch) ? branch : []
  const marked = entries.some(isDigest)
  const counts = (e) => (marked ? isDigest(e) : e.type === "custom_message" && e.customType === "deja-recall")
  let keep = ""
  for (let i = entries.length - 1; i >= 0; i--) {
    const e = entries[i] || {}
    if (keep === "" && e.type === "compaction") {
      keep = String(e.firstKeptEntryId || "")
      if (!keep) return false
      continue
    }
    if (counts(e)) return true
    if (keep && e.id === keep) return false
  }
  return false
}

// isDigest reports whether a branch entry is the start digest this package
// sent, which it marks with details.digest.
function isDigest(e) {
  return !!e && e.type === "custom_message" && e.customType === "deja-recall" && !!(e.details && e.details.digest)
}

export function branchCompactionKey(branch) {
  const entries = Array.isArray(branch) ? branch : []
  for (let i = entries.length - 1; i >= 0; i--) {
    const e = entries[i]
    if (e && e.type === "compaction") {
      return "c:" + i + ":" + String(e.id || "") + ":" + String(e.firstKeptEntryId || "")
    }
  }
  return "0"
}

function isTempDejaBin(dejaBin) {
  if (!dejaBin) return false
  const fold = (s) => {
    const clean = String(s).replace(/\\/g, "/").replace(/\/+$/, "")
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

export function promptCachePath(name = "pi-pkg-cache", dejaBin = "") {
  const override = process.env.DEJA_PROMPT_CACHE_FILE
  if (override) return override
  if (isTempDejaBin(dejaBin)) {
    return join(dirname(dejaBin), ".deja-cache", name + ".json")
  }
  const xdg = process.env.XDG_CACHE_HOME
  if (xdg && isAbsolute(xdg)) return join(xdg, "deja-vu", name + ".json")
  const base = process.platform === "win32"
    ? (process.env.LOCALAPPDATA || join(homedir(), "AppData", "Local"))
    : join(homedir(), ".cache")
  return join(base, "deja-vu", name + ".json")
}

function isPlainObject(v) {
  return Boolean(v) && typeof v === "object" && !Array.isArray(v)
}

function nullMap(src) {
  const out = Object.create(null)
  if (!isPlainObject(src)) return out
  for (const [k, v] of Object.entries(src)) out[k] = v
  return out
}

function clonePromptSession(raw) {
  if (!isPlainObject(raw)) {
    return { epoch: 0, updatedAt: 0, recalled: Object.create(null), repairs: Object.create(null), told: false, empties: 0 }
  }
  const out = {
    epoch: Number.isInteger(raw.epoch) ? raw.epoch : 0,
    updatedAt: typeof raw.updatedAt === "number" ? raw.updatedAt : 0,
    recalled: nullMap(raw.recalled),
    repairs: nullMap(raw.repairs),
    told: Boolean(raw.told),
    empties: Number.isInteger(raw.empties) ? raw.empties : 0,
  }
  if (Object.hasOwn(raw, "digest") && typeof raw.digest === "string") out.digest = raw.digest
  return out
}

function readPromptCacheFile(file) {
  try {
    if (!existsSync(file)) return { version: 1, sessions: Object.create(null) }
    const parsed = JSON.parse(readFileSync(file, "utf8"))
    if (isPlainObject(parsed) && parsed.version === 1 && isPlainObject(parsed.sessions)) {
      return { version: 1, sessions: nullMap(parsed.sessions) }
    }
  } catch {}
  return { version: 1, sessions: Object.create(null) }
}

function writePromptCacheFile(file, data, keepKey, maxSessions) {
  try {
    const entries = Object.entries(data.sessions)
    if (entries.length > maxSessions) {
      entries.sort((a, b) => ((b[1] && b[1].updatedAt) || 0) - ((a[1] && a[1].updatedAt) || 0))
      const trimmed = Object.create(null)
      if (keepKey && Object.hasOwn(data.sessions, keepKey) && data.sessions[keepKey]) {
        trimmed[keepKey] = data.sessions[keepKey]
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
    const tmp = file + "." + process.pid + "." + Math.random().toString(36).slice(2) + ".tmp"
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

export function rememberEntry(map, key, value, limit = 256) {
  if (!key) return
  if (Object.hasOwn(map, key)) delete map[key]
  map[key] = value
  const keys = Object.keys(map)
  while (keys.length > limit) {
    delete map[keys.shift()]
  }
}

export function createSessionStore(name = "pi-pkg-cache", { maxSessions = 128, dejaBin = "" } = {}) {
  const file = () => promptCachePath(name, dejaBin)
  const read = (sessionKey) => {
    const data = readPromptCacheFile(file())
    const raw = Object.hasOwn(data.sessions, sessionKey) ? data.sessions[sessionKey] : undefined
    const session = clonePromptSession(raw)
    return { epoch: session.epoch, session }
  }
  const write = (sessionKey, expectedEpoch, mutate) => {
    if (!sessionKey) return false
    const f = file()
    const data = readPromptCacheFile(f)
    const raw = Object.hasOwn(data.sessions, sessionKey) ? data.sessions[sessionKey] : undefined
    const current = clonePromptSession(raw)
    if (current.epoch !== expectedEpoch) return false
    mutate(current)
    current.updatedAt = Date.now()
    data.sessions[sessionKey] = current
    writePromptCacheFile(f, data, sessionKey, maxSessions)
    return true
  }
  const compact = (sessionKey, keepDigest) => {
    if (!sessionKey) return
    const f = file()
    const data = readPromptCacheFile(f)
    const raw = Object.hasOwn(data.sessions, sessionKey) ? data.sessions[sessionKey] : undefined
    const prev = clonePromptSession(raw)
    const next = {
      epoch: prev.epoch + 1,
      updatedAt: Date.now(),
      recalled: Object.create(null),
      repairs: Object.create(null),
      told: prev.told,
      empties: 0,
    }
    if (keepDigest && typeof prev.digest === "string") next.digest = prev.digest
    data.sessions[sessionKey] = next
    writePromptCacheFile(f, data, sessionKey, maxSessions)
  }
  return { read, write, compact }
}
