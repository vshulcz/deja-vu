import { join } from "node:path"

// The pure half of the plugin: everything that reads deja's output or decides
// what this package adds, with no process and no host. Kept apart from
// index.mjs so it can be tested without OpenClaw.

// installerPluginPath is where `deja install openclaw-auto` writes its own
// plugin. OpenClaw loads that directory and this package side by side, and
// both would push recall in front of the model, so whoever finds the other
// stands down on recall. Mirrors OpenClawStateDir() in the installer:
// OPENCLAW_STATE_DIR wins, else ~/.openclaw.
export function installerPluginPath(env, home) {
  const base = (env && env.OPENCLAW_STATE_DIR) || join(home || "", ".openclaw")
  return join(base, "extensions", "deja", "index.mjs")
}

// configPath is OpenClaw's config, where `deja install openclaw` registers the
// MCP server.
export function configPath(env, home) {
  const base = (env && env.OPENCLAW_STATE_DIR) || join(home || "", ".openclaw")
  return join(base, "openclaw.json")
}

// mcpWired reports whether that config already runs deja as an MCP server.
// The tools this package registers do the same things, so when the server is
// there they are a second copy in the model's tool list. The file may be
// JSONC; a config this cannot read is treated as not wired — losing the tools
// on a file we misread is worse than listing them twice.
export function mcpWired(text) {
  try {
    const config = JSON.parse(stripJSONComments(String(text || "")))
    const servers = config && config.mcp && config.mcp.servers
    return Boolean(servers && servers.deja)
  } catch {
    return false
  }
}

// stripJSONComments removes // and /* */ outside strings.
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
// already wired and what the user turned off in the plugin config: fill the
// gaps, never repeat the installer.
export function contributions(wiring, config = {}) {
  const wired = wiring || {}
  return {
    tools: config.tools !== false && !wired.mcp,
    recall: config.autoRecall !== false && !wired.recall,
  }
}

// accessNote is what to tell the person when this plugin's entry does not
// grant conversation access. OpenClaw 2026.8.1 and later drop a non-bundled
// plugin's before_prompt_build without it (registerTypedHook in
// src/plugins/loader, 2026.9.9), and the plugin cannot grant it to itself:
// it is a config key, not a manifest field. "" when it is granted.
export function accessNote(hostConfig, id) {
  const entry = hostConfig && hostConfig.plugins && hostConfig.plugins.entries && hostConfig.plugins.entries[id]
  if (entry && entry.hooks && entry.hooks.allowConversationAccess === true) return ""
  return (
    "OpenClaw 2026.8.1 and later run deja's per-prompt recall only with " +
    `plugins.entries.${id}.hooks.allowConversationAccess set to true: ` +
    `openclaw config set plugins.entries.${id}.hooks.allowConversationAccess true`
  )
}

// argv builds the call for a query somebody typed. A query that starts with a
// dash is read by deja as a flag and the call fails, which run() turns into
// "nothing in the history" — a wrong answer where an error would at least be
// visible. `--` ends the flags; sent only when the query needs it.
export function argv(cmd, flags, text) {
  const arg = String(text)
  return arg.startsWith("-") ? [cmd, ...flags, "--", arg] : [cmd, ...flags, arg]
}

// promptText is what the person typed on this turn, from the shapes OpenClaw
// hands before_prompt_build. Empty when there is nothing to rank against.
export function promptText(event) {
  const p = event && event.prompt
  if (typeof p === "string") return p.trim()
  if (Array.isArray(p)) return p.filter((x) => typeof x === "string").join("\n").trim()
  return ""
}

// toolCall is the deja call for a finished tool, or null when there is none:
// a file's history after a read or an edit, and after a command the repair
// this machine ran after the same error before. The same mapping as the
// installer's plugin (#4808).
export function toolCall(event, at) {
  const name = (event && event.toolName) || ""
  const args = (event && event.args) || {}
  const base = { session_id: at.id, cwd: (event && event.cwd) || at.cwd }
  if (name === "read" || name === "edit" || name === "write") {
    const path = typeof args.path === "string" ? args.path : typeof args.file_path === "string" ? args.file_path : ""
    if (!path) return null
    return { args: ["hook-tool", "--plain"], payload: { tool_name: name === "read" ? "read" : "edit", tool_input: { file_path: path }, ...base } }
  }
  if (name === "apply_patch" && typeof args.input === "string") {
    return { args: ["hook-tool", "--plain"], payload: { tool_name: "apply_patch", tool_input: { command: args.input }, ...base } }
  }
  if (name === "exec" || name === "bash") {
    const content = Array.isArray(event && event.result && event.result.content) ? event.result.content : []
    const out = content.filter((c) => c && c.type === "text" && typeof c.text === "string").map((c) => c.text).join("\n")
    if (!out.trim()) return null
    return {
      args: ["hook-tool-after", "--plain"],
      payload: { tool_name: "bash", tool_input: { command: String(args.command || "") }, tool_response: out, ...base },
    }
  }
  return null
}

// splitHookReply reads what `deja hook-prompt` or `deja hook-context` printed
// in its JSON form and splits it by reader: additionalContext is for the model,
// the same text --plain prints, and systemMessage is deja's lines for the
// person. One spawn answers both. The envelope is the last line that parses as
// one; output with no envelope is all context, so a deja that printed plain
// text still reaches the model.
export function splitHookReply(stdout) {
  const text = String(stdout || "").trim()
  if (!text) return { context: "", notes: "" }
  const lines = text.split("\n")
  for (let i = lines.length - 1; i >= 0; i--) {
    const line = lines[i].trim()
    if (!line.startsWith("{")) continue
    let reply
    try {
      reply = JSON.parse(line)
    } catch {
      continue
    }
    if (!reply || typeof reply !== "object" || Array.isArray(reply)) continue
    if (!("hookSpecificOutput" in reply) && !("systemMessage" in reply)) continue
    const out = reply.hookSpecificOutput
    const context = out && typeof out.additionalContext === "string" ? out.additionalContext.trim() : ""
    const notes = typeof reply.systemMessage === "string" ? reply.systemMessage.trim() : ""
    return { context, notes }
  }
  return { context: text, notes: "" }
}

// deliversNotes says whether a turn on this channel ends in a reply the
// person reads as it was delivered. A channel's reply goes through
// reply_payload_sending on its way out, so deja's receipt can ride along as a
// footer. The TUI and the Control UI chat are both "webchat": they draw the
// transcript, and a footer added on the way out is never shown there. Neither
// is a turn with no channel at all, such as `openclaw agent`.
export function deliversNotes(channel) {
  const name = String(channel || "").trim().toLowerCase()
  return name !== "" && name !== "webchat"
}

// ownerSender says whether a sender is the person whose history deja reads.
// commands.ownerAllowFrom names the owner as "channel:id"; without it, a
// channel allowlist of exactly one id is that one person. Anything else — a
// wildcard, several people, no list — is not known to be the owner, and the
// answer is no: the receipt quotes their past work.
export function ownerSender(config, channel, senderId) {
  const ch = String(channel || "").trim().toLowerCase()
  const id = String(senderId ?? "").trim()
  if (!ch || !id) return false
  const owners = config && config.commands && config.commands.ownerAllowFrom
  if (Array.isArray(owners) && owners.length > 0) {
    return owners.some((e) => String(e).trim().toLowerCase() === ch + ":" + id.toLowerCase())
  }
  const allow = config && config.channels && config.channels[ch] && config.channels[ch].allowFrom
  if (!Array.isArray(allow)) return false
  const entries = allow.map((e) => String(e).trim()).filter(Boolean)
  if (entries.length !== 1 || entries[0] === "*") return false
  return entries[0] === id || entries[0].toLowerCase() === ch + ":" + id.toLowerCase()
}

// directTurns remembers, per session, whether the message that started its
// turn came in a 1:1 chat and from whom. before_dispatch is the hook that is
// told isGroup; the prompt hook after it is not. A turn the board never heard
// about counts as not direct, and so does one older than a few minutes.
export function directTurns({ ttlMs = 5 * 60 * 1000, sessions = 256, now = Date.now } = {}) {
  const turns = new Map()
  return {
    heard(event, ctx) {
      const key = (event && event.sessionKey) || (ctx && ctx.sessionKey) || ""
      if (!key) return
      turns.delete(key)
      turns.set(key, {
        direct: Boolean(event) && event.isGroup === false,
        sender: String((event && event.senderId) ?? (ctx && ctx.senderId) ?? ""),
        channel: String((event && event.channel) || (ctx && ctx.channelId) || "").toLowerCase(),
        at: now(),
      })
      while (turns.size > sessions) turns.delete(turns.keys().next().value)
    },
    direct(key, channel, senderId) {
      const t = key && turns.get(key)
      if (!t || now() - t.at > ttlMs || !t.direct) return false
      if (t.channel !== String(channel || "").toLowerCase()) return false
      return !senderId || t.sender === String(senderId)
    },
  }
}

const RECEIPT = /^deja-vu\s+—\s+(you have been here|this was done on another machine):\s+("(?:[^"\\]|\\.)*")\s+\((.+)\)$/

// receiptLine turns deja's per-prompt receipt into the one line a chat shows:
// no brand in front, no "via:" terms, the date kept. The block it rides in
// has no byline, so a small "deja" at the end says whose line it is. "" when
// the text holds no receipt.
export function receiptLine(text) {
  for (const raw of String(text || "").split("\n")) {
    const m = RECEIPT.exec(raw.trim())
    if (!m) continue
    let topic = m[2]
    try {
      topic = JSON.stringify(JSON.parse(m[2]))
    } catch {}
    const when = m[3].split(/\s+·\s+via:\s+/)[0].trim()
    const who = m[1] === "you have been here" ? "you've been here" : "done on another machine"
    return "↺ " + who + ": " + topic + (when ? " · " + when : "") + " · deja"
  }
  return ""
}

// receiptBoard holds a session's receipt between the prompt hook and the
// reply that carries it, under the session key and the channel. Each turn sets
// it, a silent turn clears it, so a reply never carries an older turn's line.
// A reply that never goes out (NO_REPLY, a cancelled send) never takes it, and
// it lapses after a few minutes.
export function receiptBoard({ ttlMs = 5 * 60 * 1000, sessions = 256, now = Date.now } = {}) {
  const board = new Map()
  return {
    set(key, channel, line) {
      if (!key) return
      board.delete(key)
      if (line) board.set(key, { channel: String(channel || "").toLowerCase(), line, at: now() })
      while (board.size > sessions) board.delete(board.keys().next().value)
    },
    take(key, channel) {
      const held = key && board.get(key)
      if (!held || held.channel !== String(channel || "").toLowerCase()) return ""
      board.delete(key)
      return now() - held.at <= ttlMs ? held.line : ""
    },
  }
}

// withReceipt adds the receipt under a reply as a context block, the small
// footer OpenClaw's message presentation defines. It is set in italics the way
// the agent's own markdown would set one, since most channels draw the block
// as a plain last paragraph; a line that already holds _ or * is left as it
// is. The reply's own text and blocks are untouched.
export function withReceipt(payload, line) {
  if (!payload || !line) return payload
  const text = /[_*]/.test(line) ? line : "_" + line + "_"
  const presentation = payload.presentation && typeof payload.presentation === "object" ? payload.presentation : {}
  const blocks = Array.isArray(presentation.blocks) ? presentation.blocks : []
  return { ...payload, presentation: { ...presentation, blocks: [...blocks, { type: "context", text }] } }
}

// where names the session and workspace a hook runs for, the way the
// installer's plugin does. The id is the transcript id, not the session key:
// hook-context stamps the session live under that id, which keeps it out of
// its own MCP recall, and agent:main:main names no transcript (#4582). The
// automatic compaction inside a run hands its hook only the session key, so
// what each key last ran as is kept in seen.
export function where(seen, event, ctx, cwd) {
  const key = (ctx && ctx.sessionKey) || ""
  const last = (key && seen.get(key)) || {}
  const id =
    (ctx && ctx.sessionId) ||
    last.id ||
    (event && (event.sessionId || event.session_id || (event.session && event.session.id))) ||
    ""
  const dir = (ctx && ctx.workspaceDir) || last.dir || ""
  if (key && (id || dir)) seen.set(key, { id, dir })
  return { id: id || key, cwd: dir || cwd }
}
