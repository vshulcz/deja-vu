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

// clampLimit keeps a model that asks for a hundred sessions from spending the
// window on a tail nobody reads.
export function clampLimit(value, fallback = 5) {
  const asked = Number(value)
  if (!Number.isFinite(asked)) return fallback
  return Math.min(20, Math.max(1, Math.trunc(asked)))
}
