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

// recallArgv is the call behind the deja_recall tool: `deja recall`, the page
// the MCP recall tool serves, framed and held to its 4 KB budget by deja
// itself. The tool used to run `search`, whose page grows with the hits: 21 KB
// for twenty, against 4 KB from the MCP tool for the same question (#5568).
// The page ends with "call recall again with offset=N"; offset is
// how the tool asks for that, and it is sent only past the first page, so a
// deja too old to know --offset still answers the first one.
export function recallArgv(args) {
  const count = (v, fallback) => {
    const n = Math.trunc(Number(v))
    return v == null || v === "" || !Number.isFinite(n) ? fallback : n
  }
  const limit = Math.min(20, Math.max(1, count(args && args.limit, 5)))
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

// splitHookReply reads what `deja hook-prompt` printed in its JSON form and
// splits it by reader: additionalContext is for the model, the same text
// --plain prints, and systemMessage is deja's line for the person. One spawn
// answers both. The envelope is the last line that parses as one. Any other
// line deja printed would have been printed by --plain too, so it stays with
// the model's text, where --plain would have put it; output with no envelope
// is all context.
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
    const before = lines.slice(0, i).join("\n").trim()
    const after = lines.slice(i + 1).join("\n").trim()
    const notes = typeof reply.systemMessage === "string" ? reply.systemMessage.trim() : ""
    return { context: [before, context, after].filter(Boolean).join("\n"), notes }
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
// channel allowlist of exactly one id is that one person. Anything else (a
// wildcard, several people, no list) is not known to be the owner, and the
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

// directSessionKey says whether a session key can only be a 1:1 chat's: one
// that names a group, a channel, a thread or a topic cannot. agent:main:main,
// where every DM lands by default, passes here; the turn itself still has to
// say it was 1:1.
export function directSessionKey(key) {
  const parts = String(key || "").trim().toLowerCase().split(":")
  if (!parts[0]) return false
  return !parts.some((p) => p === "group" || p === "channel" || p === "thread" || p === "topic")
}

// conversationOf names a conversation the same way whichever hook reported
// it: before_dispatch says "4242", the reply's hook "telegram:4242". The
// prefixes are the ones OpenClaw strips for before_dispatch (stripChannelPrefix:
// "channel:", "chat:", "user:", then "<channel>:"), applied after the channel's
// own, so a Slack "slack:channel:D1" and a "D1" meet.
export function conversationOf(id, channel) {
  let s = String(id ?? "").trim().toLowerCase()
  const own = String(channel || "").trim().toLowerCase() + ":"
  if (s.startsWith(own)) s = s.slice(own.length)
  for (const prefix of ["channel:", "chat:", "user:"]) {
    if (s.startsWith(prefix)) return s.slice(prefix.length)
  }
  return s
}

// directTurn is what before_dispatch says about the message that starts a
// turn, kept so the prompt hook can tell whether that turn is a 1:1 chat. It
// is 1:1 only if every signal OpenClaw gives agrees: isGroup is false, not
// missing; the session key is not a group's; a chat type, when there is one,
// is "direct". Anything missing or unknown counts as not 1:1.
export function directTurn(event, ctx) {
  const e = event || {}
  const c = ctx || {}
  const key = e.sessionKey || c.sessionKey || ""
  const channel = String(e.channel || c.channelId || "").trim().toLowerCase()
  const chatType = e.chatType ?? c.chatType
  const conversation = conversationOf(c.conversationId ?? e.conversationId, channel)
  const direct =
    e.isGroup === false &&
    directSessionKey(key) &&
    (chatType === undefined || chatType === "direct") &&
    channel !== "" &&
    conversation !== ""
  return { key, direct, channel, conversation, sender: String(e.senderId ?? c.senderId ?? "").trim() }
}

// wholeReplies says whether a channel is known to send a reply whole, as one
// final message. A channel that streams it (Telegram's default preview
// streaming, block streaming) sends it in chunks and never a final reply the
// receipt could ride on, and deja marks a receipt shown when it prints it, so
// there it is not asked for. Only streaming set off, with no block streaming
// turned on for the channel, an account or by default, counts; anything else
// is taken for streaming.
export function wholeReplies(config, channel) {
  const ch = String(channel || "").trim().toLowerCase()
  const section = config && config.channels && config.channels[ch]
  if (!ch || !section) return false
  const off = (s) => s === "off" || s === false || (s && typeof s === "object" && s.mode === "off" && !(s.block && s.block.enabled === true))
  // blockStreaming: true is the older spelling of streaming.block.enabled.
  if (!off(section.streaming) || section.blockStreaming === true) return false
  const accounts = section.accounts && typeof section.accounts === "object" ? Object.values(section.accounts) : []
  if (accounts.some((a) => a && ((a.streaming !== undefined && !off(a.streaming)) || a.blockStreaming === true))) return false
  const defaults = config.agents && config.agents.defaults
  return !(defaults && defaults.blockStreamingDefault === "on")
}

// receiptAllowed is the whole rule for asking deja for the receipt on this
// turn: a 1:1 turn heard by before_dispatch, on a channel that delivers its
// reply, from the owner, in the same session, with a run id to tie the reply
// to. Every condition fails closed.
export function receiptAllowed(turn, ctx, channel, config) {
  const c = ctx || {}
  if (!turn || !turn.direct) return false
  if (!deliversNotes(channel) || turn.channel !== String(channel).trim().toLowerCase()) return false
  if (!c.sessionKey || turn.key !== c.sessionKey || !directSessionKey(c.sessionKey)) return false
  if (c.chatType !== undefined && c.chatType !== "direct") return false
  if (!c.runId || !turn.sender || String(c.senderId ?? "").trim() !== turn.sender) return false
  if (!wholeReplies(config, channel)) return false
  return ownerSender(config, channel, turn.sender)
}

// replyTakes says whether a reply on its way out is the one the receipt was
// kept for: the final reply of the same run, to the same conversation, on the
// same channel, that the delivery itself calls direct, carrying text. A block-stream chunk, a media
// or voice reply, or anything that does not match gets nothing.
export function replyTakes(held, event, ctx) {
  const e = event || {}
  const c = ctx || {}
  if (!held || e.kind !== "final") return false
  const channel = String(e.channel || c.channelId || "").trim().toLowerCase()
  if (channel !== held.channel) return false
  if (!held.runId || (e.runId || c.runId) !== held.runId) return false
  if (conversationOf(c.conversationId, channel) !== held.conversation) return false
  if (c.senderId !== undefined && String(c.senderId).trim() !== held.sender) return false
  // The delivery's own word that it is a 1:1 chat, required: a session key
  // with no group word in it (session.scope "global") says nothing.
  if (!e.usageState || e.usageState.chatType !== "direct") return false
  const p = e.payload || {}
  if (p.audioAsVoice === true) return false
  return typeof p.text === "string" && p.text.trim() !== ""
}

// escapeMarkdown keeps a quoted topic from being read as formatting: a
// topic with an underscore or a star would otherwise end the italics early.
export function escapeMarkdown(text) {
  return String(text).replace(/[\\\x60*_~|\[\]]/g, "\\$&")
}

// receiptLine turns deja's per-prompt receipt into the one line a chat shows:
// no brand in front, no "via" terms, the date kept. The block it rides in has
// no byline, so a small "deja" at the end says whose line it is. "" when the
// text holds no receipt.
export function receiptLine(text) {
  const shape = /^deja-vu\s+—\s+(you have been here|this was done on another machine):\s+("(?:[^"\\]|\\.)*")\s+\((.+)\)$/
  for (const raw of String(text || "").split("\n")) {
    const m = shape.exec(raw.trim())
    if (!m) continue
    let topic = m[2].slice(1, -1)
    try {
      topic = JSON.parse(m[2])
    } catch {}
    const when = m[3].split(/\s+·\s+via:?\s+/)[0].trim()
    const who = m[1] === "you have been here" ? "you've been here" : "done on another machine"
    return "↺ " + who + ': "' + escapeMarkdown(topic) + '"' + (when ? " · " + escapeMarkdown(when) : "") + " · deja"
  }
  return ""
}

// withReceipt adds the receipt under a reply as a context block, the small
// footer OpenClaw's message presentation defines, set in italics the way the
// agent's own markdown would set one, since most channels draw the block as a
// plain last paragraph. The reply's own text and blocks are untouched.
export function withReceipt(payload, line) {
  if (!payload || !line) return payload
  const presentation = payload.presentation && typeof payload.presentation === "object" ? payload.presentation : {}
  const blocks = Array.isArray(presentation.blocks) ? presentation.blocks : []
  return { ...payload, presentation: { ...presentation, blocks: [...blocks, { type: "context", text: "_" + line + "_" }] } }
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
