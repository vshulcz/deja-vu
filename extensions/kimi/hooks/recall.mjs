#!/usr/bin/env node
// Runs one deja hook with Kimi's payload on stdin, the same hooks
// `deja install kimi-auto` writes into config.toml: the digest on the first
// prompt (hook-context --once), recall on every prompt (hook-prompt), and the
// compaction capture (hook-precompact --harness kimi), and the end of the
// session (hook-session-end, which drops its live stamp). The manifest names the
// subcommand; with none it is hook-prompt, which is what this file ran before
// it took arguments. Kimi appends a UserPromptSubmit hook's stdout to the
// turn's context, which is the whole mechanism — its structured output only
// carries permission decisions.
//
// Silence is the normal case. Nothing here may cost the user a turn: every
// failure exits 0 with no output, which Kimi treats as "nothing to add".

import { spawn } from "node:child_process"
import { hookArgs, installerOwns, resolveDeja } from "../lib.mjs"

const TIMEOUT_MS = 20000

async function main() {
  // `deja install kimi-auto` writes the same hook into config.toml. Kimi runs
  // both, and the user would read the same recall twice, every prompt.
  if (installerOwns("hook")) return

  const payload = await readStdin()
  if (!payload.trim()) return

  const out = await run(resolveDeja(), hookArgs(process.argv.slice(2)), payload)
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
