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
  process.exit(0)
}

const child = spawn(resolveDeja(), ["mcp"], { stdio: "inherit" })
child.on("error", (err) => {
  process.stderr.write(
    `deja could not start: ${err.message}. Install it with: ` +
      "curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh\n",
  )
  process.exit(1)
})
child.on("close", (code) => process.exit(code ?? 0))
