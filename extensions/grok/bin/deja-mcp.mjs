#!/usr/bin/env node
// The plugin's MCP server: `deja mcp` over stdio, with the binary resolved the
// way the user expects rather than pinned to whatever this release bundled.
//
// A plugin server and the `[mcp_servers.deja]` entry `deja install grok` writes
// are two servers to Grok, not one — both would run and the agent would see
// every tool twice. When the installer has already wired it, this stands down
// and says why: the CLI's copy is the one `deja install` keeps current.

import { spawn } from "node:child_process"
import { grokHome, installerOwns, resolveDeja } from "../lib.mjs"

if (installerOwns("mcp")) {
  process.stderr.write(
    `deja is already an MCP server in ${grokHome()}/config.toml, from \`deja install grok\`. ` +
      "This plugin's copy stays out of the way so the tools are not listed twice — " +
      "run `deja uninstall grok` to let the plugin own it instead.\n",
  )
  standDown()
} else {
  start()
}

// Standing down by exiting looks like a crash from the outside: Grok Build 1.0.5
// reports `handshake failed: connection closed: initialize response` and the
// server sits in the UI as broken. So answer the handshake and offer nothing —
// the tools come from the CLI-wired copy, and this one is visibly idle rather
// than visibly failing.
function standDown() {
  let buf = ""
  process.stdin.setEncoding("utf8")
  process.stdin.on("data", (chunk) => {
    buf += chunk
    let i
    while ((i = buf.indexOf("\n")) >= 0) {
      const line = buf.slice(0, i)
      buf = buf.slice(i + 1)
      if (line.trim()) reply(line)
    }
  })
  process.stdin.on("end", () => process.exit(0))
}

function reply(line) {
  let msg
  try {
    msg = JSON.parse(line)
  } catch {
    return
  }
  if (msg.id === undefined) return // a notification wants no answer
  let result
  switch (msg.method) {
    case "initialize":
      result = {
        protocolVersion: msg.params?.protocolVersion ?? "2024-11-05",
        capabilities: { tools: {} },
        serverInfo: { name: "deja (plugin, standing down)", version: "0.1.0" },
      }
      break
    case "tools/list":
      result = { tools: [] }
      break
    case "resources/list":
      result = { resources: [] }
      break
    case "prompts/list":
      result = { prompts: [] }
      break
    case "ping":
      result = {}
      break
    default:
      process.stdout.write(
        JSON.stringify({ jsonrpc: "2.0", id: msg.id, error: { code: -32601, message: "not served: deja is wired through the CLI" } }) + "\n",
      )
      return
  }
  process.stdout.write(JSON.stringify({ jsonrpc: "2.0", id: msg.id, result }) + "\n")
}

function start() {
  const child = spawn(resolveDeja(), ["mcp"], { stdio: "inherit" })
  child.on("error", (err) => {
    process.stderr.write(
      `deja could not start: ${err.message}. Install it with: ` +
        "curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh\n",
    )
    process.exit(1)
  })
  child.on("close", (code) => process.exit(code ?? 0))
}
