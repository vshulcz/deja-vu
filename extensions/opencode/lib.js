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

// lastUserText is what the person typed on this turn, which is what the
// per-prompt recall is ranked against. Parts a plugin appended earlier are
// part of the same message, so the text is joined and matched whole.
export function lastUserText(messages) {
  const list = Array.isArray(messages) ? messages : []
  for (let i = list.length - 1; i >= 0; i--) {
    if (list[i]?.info?.role !== "user") continue
    const parts = (list[i].parts || []).filter((p) => p?.type === "text" && p.text)
    if (!parts.length) return { parts: [], prompt: "" }
    return { parts, prompt: parts.map((p) => p.text).join("\n").trim() }
  }
  return { parts: [], prompt: "" }
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
    return Boolean(config && config.mcp && config.mcp.deja)
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

// clampLimit keeps a model that asks for a hundred sessions from spending the
// window on a tail nobody reads.
export function clampLimit(value, fallback = 5) {
  const asked = Number(value)
  if (!Number.isFinite(asked)) return fallback
  return Math.min(20, Math.max(1, Math.trunc(asked)))
}
