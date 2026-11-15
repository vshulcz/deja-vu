// Pure path, binary resolution, stand-down, and prompt/input helpers for the
// Orca v2 trusted-worker plugin.

import { existsSync, readFileSync } from "node:fs"
import { homedir } from "node:os"
import { join } from "node:path"

export function orcaRoots(env = process.env, home = homedir(), platform = process.platform) {
  const override = env.DEJA_ORCA_ROOT?.trim()
  if (override) return [override]
  if (platform === "darwin") {
    return [join(home, "Library", "Application Support", "orca")]
  }
  if (platform === "win32") {
    const base = env.APPDATA || join(home, "AppData", "Roaming")
    return [join(base, "orca"), join(base, "Orca")]
  }
  const base = env.XDG_CONFIG_HOME || join(home, ".config")
  return [join(base, "orca"), join(base, "Orca")]
}

export function wellKnown(home = homedir(), platform = process.platform) {
  if (platform === "win32") {
    return [
      join(envLocalAppData(), "Programs", "deja", "deja.exe"),
      join(home, ".local", "bin", "deja.exe"),
      join(home, "bin", "deja.exe"),
    ]
  }
  return [
    "/opt/homebrew/bin/deja",
    "/usr/local/bin/deja",
    join(home, ".local", "bin", "deja"),
    join(home, "bin", "deja"),
  ]
}

function envLocalAppData() {
  return process.env.LOCALAPPDATA || join(homedir(), "AppData", "Local")
}

export function resolveDeja(
  env = process.env,
  home = homedir(),
  exists = existsSync,
  platform = process.platform,
) {
  const fromEnv = env.DEJA_BIN?.trim()
  if (fromEnv) return fromEnv
  for (const candidate of wellKnown(home, platform)) {
    if (exists(candidate)) return candidate
  }
  return platform === "win32" ? "deja.exe" : "deja"
}

export function userPromptHookPresent(raw) {
  if (!raw || typeof raw !== "string") return false
  let parsed
  try {
    parsed = JSON.parse(raw)
  } catch {
    return false
  }
  const entries = parsed?.hooks?.UserPromptSubmit
  if (!Array.isArray(entries)) return false
  return hasHookCommand(entries, "hook-prompt")
}

function hasHookCommand(node, needle) {
  if (typeof node === "string") return node.includes(needle)
  if (Array.isArray(node)) return node.some((item) => hasHookCommand(item, needle))
  if (node && typeof node === "object") {
    return Object.values(node).some((val) => hasHookCommand(val, needle))
  }
  return false
}

export function mcpPresent(raw) {
  if (!raw || typeof raw !== "string") return false
  let parsed
  try {
    parsed = JSON.parse(raw)
  } catch {
    return false
  }
  return Boolean(parsed?.mcpServers && Object.prototype.hasOwnProperty.call(parsed.mcpServers, "deja"))
}

// Stand down when `deja install orca` already wired Orca's native settings.json
// UserPromptSubmit hook so recall does not appear twice in a prompt.
export function installerOwns(
  kind,
  env = process.env,
  home = homedir(),
  platform = process.platform,
  readFile = (p) => readFileSync(p, "utf8"),
) {
  const roots = orcaRoots(env, home, platform)
  for (const root of roots) {
    if (kind === "hook-prompt" || kind === "hook") {
      try {
        if (userPromptHookPresent(readFile(join(root, "settings.json")))) return true
      } catch {
        // Missing or unreadable config counts as absent.
      }
    }
    if (kind === "mcp") {
      try {
        if (mcpPresent(readFile(join(root, "mcp.json")))) return true
      } catch {
        // Missing or unreadable config counts as absent.
      }
    }
  }
  return false
}

export function appendRecall(prompt, recall) {
  if (typeof prompt !== "string") return prompt
  const trimmed = typeof recall === "string" ? recall.trim() : ""
  if (!trimmed) return prompt
  if (prompt.includes("<deja-recall")) return prompt
  if (!prompt.trim()) return trimmed
  return `${prompt}\n\n${trimmed}`
}

export function isSubagentTool(tool) {
  if (typeof tool !== "string") return false
  const lower = tool.trim().toLowerCase()
  return lower === "agent" || lower === "task"
}

export function rewriteSubagentInput(tool, input, recall) {
  if (!isSubagentTool(tool)) return null
  if (!input || typeof input !== "object" || Array.isArray(input)) return null
  if (typeof input.prompt !== "string" || !input.prompt.trim()) return null
  const updated = appendRecall(input.prompt, recall)
  if (updated === input.prompt) return null
  return { ...input, prompt: updated }
}

export function parseRecallArgs(args) {
  if (typeof args === "string") return args.trim()
  if (Array.isArray(args)) {
    return args.filter((item) => typeof item === "string" && item.trim()).join(" ").trim()
  }
  if (args && typeof args === "object") {
    for (const key of ["query", "text", "prompt", "q"]) {
      if (typeof args[key] === "string" && args[key].trim()) {
        return args[key].trim()
      }
    }
  }
  return ""
}

export function formatNotificationBody(text, maxLen = 240) {
  if (typeof text !== "string") return ""
  const clean = text.replace(/\s+/g, " ").trim()
  if (!clean) return ""
  if (clean.length <= maxLen) return clean
  return `${clean.slice(0, maxLen - 1)}…`
}
