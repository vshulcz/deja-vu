#!/usr/bin/env node
// UserPromptSubmit: hand Grok the sessions this machine already has that match
// what the user just asked. Grok reads hooks in Claude Code's shape and feeds a
// hook's stdout back as context, which is the whole mechanism.
//
// Silence is the normal case, and nothing here may cost a turn: every failure
// exits 0 with no output, which Grok treats as "nothing to add".

import { spawn } from "node:child_process"
import { installerOwns, resolveDeja } from "../lib.mjs"

const TIMEOUT_MS = 20000

async function main() {
  // `deja install grok-auto` writes the same hook into ~/.grok/hooks/deja.json.
  // Grok runs every file in that directory, so without this the user reads the
  // same recall twice on every prompt.
  if (installerOwns("hook")) return

  const payload = await readStdin()
  if (!payload.trim()) return

  const out = await run(resolveDeja(), ["hook-prompt", "--plain"], payload)
  if (out.trim()) process.stdout.write(out)
}

function readStdin() {
  return new Promise((resolve) => {
    let data = ""
    if (process.stdin.isTTY) {
      resolve("")
      return
    }
    process.stdin.setEncoding("utf8")
    process.stdin.on("data", (chunk) => {
      data += chunk
    })
    process.stdin.on("end", () => resolve(data))
    process.stdin.on("error", () => resolve(""))
  })
}

function run(bin, args, input) {
  return new Promise((resolve) => {
    let out = ""
    let settled = false
    const done = (value) => {
      if (settled) return
      settled = true
      resolve(value)
    }
    let child
    try {
      child = spawn(bin, args, { stdio: ["pipe", "pipe", "ignore"] })
    } catch {
      done("")
      return
    }
    const timer = setTimeout(() => {
      child.kill("SIGKILL")
      done("")
    }, TIMEOUT_MS)
    child.stdout.setEncoding("utf8")
    child.stdout.on("data", (chunk) => {
      out += chunk
    })
    child.on("error", () => {
      clearTimeout(timer)
      done("")
    })
    child.on("close", (code) => {
      clearTimeout(timer)
      done(code === 0 ? out : "")
    })
    child.stdin.on("error", () => {})
    child.stdin.end(input)
  })
}

main().catch(() => {})
