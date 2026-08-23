// The pure half of the Kimi Code plugin: where the binary is, and what
// `deja install kimi` has already wired up. No process, no host — index.js has
// no place here because Kimi loads a plugin as data, not as a module; the two
// entry points are hooks/recall.mjs and bin/deja-mcp.mjs.

import { homedir } from "node:os"
import { join } from "node:path"
import { accessSync, constants, readFileSync } from "node:fs"

const WINDOWS = process.platform === "win32"
export const EXE = WINDOWS ? "deja.exe" : "deja"

// kimiHome mirrors sources.KimiConfigDir() in the installer: KIMI_CODE_HOME
// wins, else ~/.kimi-code. Kimi sets that variable for plugin hooks and plugin
// MCP servers, so the two always agree on which config they are reading.
export function kimiHome(env = process.env, home = homedir()) {
  return env.KIMI_CODE_HOME || join(home, ".kimi-code")
}

// wellKnown is where a user's own install lands. PATH answers first in the
// normal case; these cover a Kimi started from a launcher whose PATH never
// sourced a shell profile.
export function wellKnown(home = homedir(), platform = process.platform) {
  if (platform === "win32") {
    const local = process.env.LOCALAPPDATA || join(home, "AppData", "Local")
    return [join(local, "deja", "bin", "deja.exe"), join(home, ".local", "bin", "deja.exe")]
  }
  return [
    join(home, ".local", "bin", "deja"),
    "/usr/local/bin/deja",
    "/opt/homebrew/bin/deja",
    "/usr/bin/deja",
  ]
}

// resolveDeja picks the binary in the order a user would expect: what they
// pointed at, then the deja they installed themselves and keep current with
// `deja update` or brew, and only then a copy shipped alongside. Their own
// update has to win over whatever a plugin release froze.
export function resolveDeja(env = process.env, home = homedir(), exists = executable) {
  const candidates = [env.DEJA_BIN, ...wellKnown(home)]
  for (const candidate of candidates) {
    if (candidate && exists(candidate)) return candidate
  }
  // Nothing on disk answered. The bare name keeps PATH in play and makes the
  // failure name the thing that is missing rather than a path nobody chose.
  return EXE
}

function executable(path) {
  try {
    accessSync(path, constants.X_OK)
    return true
  } catch {
    return false
  }
}

// installerHookMarker is the comment `deja install kimi-auto` writes above its
// own [[hooks]] entry. Kimi runs every matching hook, so without this check a
// user who ran the installer and then added the plugin would get the same
// recall appended twice on every prompt.
export const installerHookMarker = "# deja: auto-recall (managed by `deja install kimi-auto`)"

// The marker alone is not enough: an install from an older deja can leave a
// block that no longer calls anything Kimi consumes — its output went to
// SessionStart, which Kimi runs and then ignores. Standing down for a block
// like that would mean no recall at all, from either half. `deja doctor` calls
// the same state stale, and reads this file the same way.
export function installerHookPresent(configToml) {
  const text = String(configToml || "")
  const at = text.indexOf(installerHookMarker)
  if (at < 0) return false
  return blockAfter(text.slice(at + installerHookMarker.length)).includes("hook-prompt")
}

// blockAfter returns the marked entry and stops at the next table header, so a
// [[hooks]] rule the user wrote below ours is not read as part of it.
function blockAfter(rest) {
  const lines = []
  let started = false
  for (const line of rest.split("\n")) {
    const header = line.trimStart().startsWith("[")
    if (header && started) break
    if (header) started = true
    lines.push(line)
  }
  return lines.join("\n")
}

// installerMcpPresent reports whether `deja install kimi` already declared the
// server in mcp.json. Kimi namespaces a plugin's server as
// `plugin-<id>:<name>`, so it does not collide with the installer's entry — it
// runs a second `deja mcp` and shows the agent every tool twice.
export function installerMcpPresent(mcpJson) {
  const text = String(mcpJson || "").trim()
  if (!text) return false
  try {
    return Boolean(JSON.parse(text)?.mcpServers?.deja)
  } catch {
    // A config we cannot read is not evidence that deja is in it.
    return false
  }
}

export function readFileOrEmpty(path) {
  try {
    return readFileSync(path, "utf8")
  } catch {
    return ""
  }
}

// installerOwns answers the one question both entry points ask: has the CLI
// already wired this up? The installer's copy wins — it is the one
// `deja install` keeps current when the binary moves or the flags change.
export function installerOwns(what, env = process.env, home = homedir()) {
  const dir = kimiHome(env, home)
  if (what === "hook") return installerHookPresent(readFileOrEmpty(join(dir, "config.toml")))
  if (what === "mcp") return installerMcpPresent(readFileOrEmpty(join(dir, "mcp.json")))
  return false
}
