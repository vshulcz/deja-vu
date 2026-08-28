// The plugin's decisions, tested without Grok: which binary it picks, and when
// it stands down because `deja install grok` already wired the same thing.
// Each case pins a rule that was got wrong once somewhere in this repository.

import assert from "node:assert/strict"
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test from "node:test"

import { grokHome, hooksPresent, installerOwns, mcpPresent, resolveDeja, wellKnown } from "../lib.mjs"

test("grokHome follows GROK_HOME, the same variable the installer reads", () => {
  assert.equal(grokHome({ GROK_HOME: "/tmp/grok" }, "/home/x"), "/tmp/grok")
  assert.equal(grokHome({}, "/home/x"), join("/home/x", ".grok"))
})

test("the user's own binary wins over anything a release froze", () => {
  const exists = (p) => p === "/opt/homebrew/bin/deja"
  assert.equal(resolveDeja({}, "/home/x", exists), "/opt/homebrew/bin/deja")
  assert.equal(resolveDeja({ DEJA_BIN: "/custom/deja" }, "/home/x", () => true), "/custom/deja")
})

test("nothing on disk still leaves PATH in play", () => {
  assert.equal(resolveDeja({}, "/home/x", () => false), process.platform === "win32" ? "deja.exe" : "deja")
})

test("the well-known list is platform-specific", () => {
  assert.ok(wellKnown("/home/x", "win32").every((p) => p.endsWith("deja.exe")))
  assert.ok(wellKnown("/home/x", "darwin").includes("/opt/homebrew/bin/deja"))
})

// `[mcp_servers.deja]` is what `deja install grok` writes. A mention inside a
// comment or a string is not the section.
test("the MCP section is matched on its own line", () => {
  assert.equal(mcpPresent('[mcp_servers.deja]\ncommand = "deja"\n'), true)
  assert.equal(mcpPresent("  [mcp_servers.deja]  \n"), true)
  assert.equal(mcpPresent('# see [mcp_servers.deja] for the format\n'), false)
  assert.equal(mcpPresent('[mcp_servers.other]\ncommand = "x"\n'), false)
  assert.equal(mcpPresent(""), false)
})

test("a hook file that will not parse counts as absent", () => {
  assert.equal(hooksPresent('{"hooks":{"UserPromptSubmit":[{"hooks":[{"command":"deja hook-prompt"}]}]}}'), true)
  assert.equal(hooksPresent('{"hooks":{"SessionStart":[]}}'), false)
  assert.equal(hooksPresent("{not json"), false)
  assert.equal(hooksPresent(""), false)
})

test("installerOwns reads the two files the installer writes", () => {
  const dir = mkdtempSync(join(tmpdir(), "deja-grok-"))
  try {
    const env = { GROK_HOME: dir }
    assert.equal(installerOwns("mcp", env, "/home/x"), false)
    assert.equal(installerOwns("hook", env, "/home/x"), false)

    writeFileSync(join(dir, "config.toml"), '[mcp_servers.deja]\ncommand = "deja"\n')
    mkdirSync(join(dir, "hooks"), { recursive: true })
    writeFileSync(
      join(dir, "hooks", "deja.json"),
      '{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"deja hook-prompt"}]}]}}',
    )

    assert.equal(installerOwns("mcp", env, "/home/x"), true)
    assert.equal(installerOwns("hook", env, "/home/x"), true)
    assert.equal(installerOwns("nonsense", env, "/home/x"), false)
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})
