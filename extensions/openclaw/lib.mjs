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

// sessionKey is what recall dedups on: a hit shown once in a session is not
// shown again. Without a key every turn could repeat the same session.
export function sessionKey(event, ctx) {
  return (
    (ctx && (ctx.sessionKey || ctx.sessionId)) ||
    (event && (event.sessionId || event.session_id || (event.session && event.session.id))) ||
    ""
  )
}
