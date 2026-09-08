import { join } from "node:path"

// The pure half of the extension: everything that reads deja's output or
// decides what this package adds, with no process and no host, so it can be
// tested without pi.

// installerExtensionPath is where `deja install pi-auto` writes its own
// extension. pi loads that file and this package side by side, and both
// would inject recall and register /deja, so this package stands down when
// the installer's copy is present. Mirrors PiConfigDir() in the installer,
// which is ~/.pi/agent with no override — pi has none either.
export function installerExtensionPath(home) {
  return join(home || "", ".pi", "agent", "extensions", "deja.ts")
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
