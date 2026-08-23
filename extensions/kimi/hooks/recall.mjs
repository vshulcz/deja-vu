#!/usr/bin/env node
// UserPromptSubmit: hand Kimi the sessions this machine already has that match
// what the user just asked. Kimi appends a hook's stdout to the turn's context,
// which is the whole mechanism — its structured output only carries permission
// decisions.
//
// Silence is the normal case. Nothing here may cost the user a turn: every
// failure exits 0 with no output, which Kimi treats as "nothing to add".

import { spawn } from "node:child_process"
import { installerOwns, resolveDeja } from "../lib.mjs"

const TIMEOUT_MS = 20000

async function main() {
  // `deja install kimi-auto` writes the same hook into config.toml. Kimi runs
  // both, and the user would read the same recall twice, every prompt.
  if (installerOwns("hook")) return

  const payload = await readStdin()
  if (!payload.trim()) return

  const out = await run(resolveDeja(), ["hook-prompt", "--plain"], payload)
  if (out.trim()) process.stdout.write(out)
}

function readStdin() {
  return new Promise((resolve) => {
    let data = ""
    const done = () => resolve(data)
    if (process.stdin.isTTY) return resolve("")
    process.stdin.setEncoding("utf8")
    process.stdin.on("data", (chunk) => (data += chunk))
    process.stdin.on("end", done)
    process.stdin.on("error", done)
  })
}

function run(bin, args, input) {
  return new Promise((resolve) => {
    let child
    try {
      child = spawn(bin, args, { stdio: ["pipe", "pipe", "ignore"] })
    } catch {
      return resolve("")
    }
    let out = ""
    const timer = setTimeout(() => {
      child.kill()
      resolve("")
    }, TIMEOUT_MS)
    child.stdout.setEncoding("utf8")
    child.stdout.on("data", (chunk) => (out += chunk))
    child.on("error", () => {
      clearTimeout(timer)
      resolve("")
    })
    child.on("close", (code) => {
      clearTimeout(timer)
      resolve(code === 0 ? out : "")
    })
    child.stdin.on("error", () => {})
    child.stdin.end(input)
  })
}

main().then(
  () => process.exit(0),
  // Recall is optional everywhere. A plugin that throws here would be a plugin
  // that costs the user their turn.
  () => process.exit(0),
)
