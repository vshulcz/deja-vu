import { join } from "node:path"

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
export function userTurns(messages) {
  const list = Array.isArray(messages) ? messages : []
  const out = []
  for (const m of list) {
    if (m?.info?.role !== "user") continue
    const sessionID = m.info?.sessionID || ""
    const parts = (m.parts || []).filter((p) => p?.type === "text" && p.text)
    const prompt = parts.map((p) => p.text).join("\n").trim()
    out.push({ key: m.info?.id || sessionID + "#" + out.length + "#" + prompt, parts, prompt, sessionID })
  }
  return out
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
// 2.x server plugin has no channel to the TUI, so the toasts go nowhere. The
// session lookup is what names a sub-agent's parent: without it the
// sub-agent's digest and recall led with the parent that spawned it (#4548).
// 1.x answers through client.session.get, 2.x through ctx.session.get, whose
// SessionInfo carries parentID.
export function v1Client(ctx) {
  return {
    tui: { showToast: async () => {} },
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
