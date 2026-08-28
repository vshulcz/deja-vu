// Standing down has to look like an idle server, not a broken one. Exiting on
// start made Grok Build 1.0.5 log `handshake failed: connection closed:
// initialize response` and leave the server in the plugin UI as failing, on
// exactly the machines where everything was in fact wired correctly.

import assert from "node:assert/strict"
import { spawn } from "node:child_process"
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import test from "node:test"
import { fileURLToPath } from "node:url"

const launcher = join(dirname(fileURLToPath(import.meta.url)), "..", "bin", "deja-mcp.mjs")

function ask(env, requests) {
  return new Promise((resolve, reject) => {
    const child = spawn(process.execPath, [launcher], { env: { ...process.env, ...env }, stdio: ["pipe", "pipe", "ignore"] })
    let out = ""
    const timer = setTimeout(() => {
      child.kill("SIGKILL")
      reject(new Error("the launcher never answered"))
    }, 10000)
    child.stdout.setEncoding("utf8")
    child.stdout.on("data", (chunk) => {
      out += chunk
      if (out.split("\n").filter((l) => l.trim()).length >= requests.length) {
        clearTimeout(timer)
        child.kill()
        resolve(out.split("\n").filter((l) => l.trim()).map((l) => JSON.parse(l)))
      }
    })
    child.on("error", (err) => {
      clearTimeout(timer)
      reject(err)
    })
    for (const req of requests) child.stdin.write(JSON.stringify(req) + "\n")
  })
}

test("a stood-down server answers the handshake and offers no tools", async () => {
  const dir = mkdtempSync(join(tmpdir(), "deja-grok-mcp-"))
  try {
    // What `deja install grok` writes, which is what makes this copy stand down.
    writeFileSync(join(dir, "config.toml"), '[mcp_servers.deja]\ncommand = "deja"\nargs = ["mcp"]\n')
    mkdirSync(join(dir, "hooks"), { recursive: true })

    const [init, tools] = await ask({ GROK_HOME: dir }, [
      { jsonrpc: "2.0", id: 1, method: "initialize", params: { protocolVersion: "2024-11-05", capabilities: {} } },
      { jsonrpc: "2.0", id: 2, method: "tools/list", params: {} },
    ])

    assert.equal(init.id, 1)
    assert.equal(init.error, undefined)
    assert.equal(init.result.protocolVersion, "2024-11-05")
    assert.deepEqual(tools.result.tools, [])
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})
