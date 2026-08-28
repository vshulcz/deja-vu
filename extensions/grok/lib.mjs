// The pure half of the Grok Build plugin: where the binary is, and what
// `deja install grok` has already wired. No process and no host here — Grok
// loads a plugin as data, and the two entry points are bin/deja-mcp.mjs and
// hooks/recall.mjs.

import { homedir } from "node:os"
import { join } from "node:path"
import { accessSync, constants, readFileSync } from "node:fs"

const WINDOWS = process.platform === "win32"
export const EXE = WINDOWS ? "deja.exe" : "deja"

// grokHome mirrors sources.GrokHome() in the installer: GROK_HOME wins, else
// ~/.grok. Both sides have to agree on which config they are reading, or the
// stand-down below reads the wrong file and does nothing.
export function grokHome(env = process.env, home = homedir()) {
  return env.GROK_HOME || join(home, ".grok")
}

// wellKnown is where a user's own install lands. PATH answers first in the
// normal case; these cover a Grok started from a launcher whose PATH never
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
// `deja update` or brew. Their own update has to win over whatever a plugin
// release froze.
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

// `deja install grok` writes `[mcp_servers.deja]` into config.toml, and
// `deja install grok-auto` writes ~/.grok/hooks/deja.json with the four hooks
// this plugin would otherwise add a second time. Grok runs every hook file in
// that directory and every server it is given, so without these checks the
// user reads the same recall twice and sees every tool listed twice.
export function installerOwns(what, env = process.env, home = homedir()) {
  const dir = grokHome(env, home)
  if (what === "mcp") return mcpPresent(readFileOrEmpty(join(dir, "config.toml")))
  if (what === "hook") return hooksPresent(readFileOrEmpty(join(dir, "hooks", "deja.json")))
  return false
}

// The installer's TOML block is `[mcp_servers.deja]`. Matched on its own line
// so a mention inside a string or a comment is not read as the section.
export function mcpPresent(toml) {
  return String(toml)
    .split("\n")
    .some((line) => line.trim() === "[mcp_servers.deja]")
}

// The installer's hook file is deja's alone — it writes the whole file — so
// any deja command in it means the hooks are already wired.
export function hooksPresent(json) {
  const text = String(json).trim()
  if (!text) return false
  try {
    return JSON.stringify(JSON.parse(text)).includes("hook-prompt")
  } catch {
    // A file that will not parse is a file Grok will not run either. Treating
    // it as absent keeps the plugin working on a machine with a broken config
    // rather than standing down for a hook that never fires.
    return false
  }
}

function readFileOrEmpty(path) {
  try {
    return readFileSync(path, "utf8")
  } catch {
    return ""
  }
}
