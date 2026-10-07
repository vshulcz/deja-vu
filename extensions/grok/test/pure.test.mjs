// The plugin's decisions, tested without Grok: which binary it picks, and when
// it stands down because `deja install grok` already wired the same thing.
// Each case pins a rule that was got wrong once somewhere in this repository.

import assert from "node:assert/strict"
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test from "node:test"

import { grokHome, hookArgs, hooksPresent, installerOwns, mcpPresent, resolveDeja, wellKnown } from "../lib.mjs"

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

// The plugin used to wire UserPromptSubmit alone, whose output Grok drops, so
// it did nothing a user could see. hooks.json now runs the five hooks the
// installer writes, each naming its subcommand.
test("each hooks.json entry reaches deja as the subcommand it names", async () => {
  const { execFileSync } = await import("node:child_process")
  const { chmodSync, readFileSync } = await import("node:fs")
  const dir = mkdtempSync(join(tmpdir(), "deja-grok-args-"))
  try {
    const fake = join(dir, "deja")
    writeFileSync(fake, '#!/bin/sh\necho "ARGS $*"\n')
    chmodSync(fake, 0o755)
    const manifest = JSON.parse(readFileSync(new URL("../hooks/hooks.json", import.meta.url), "utf8"))
    const script = new URL("../hooks/recall.mjs", import.meta.url).pathname
    const seen = {}
    for (const [event, entries] of Object.entries(manifest.hooks)) {
      for (const entry of entries) {
        for (const hook of entry.hooks) {
          const args = hook.command.split(" ").slice(2)
          const out = execFileSync(process.execPath, [script, ...args], {
            input: JSON.stringify({ hookEventName: event, sessionId: "s1" }),
            encoding: "utf8",
            env: { ...process.env, DEJA_BIN: fake, GROK_HOME: dir },
          })
          seen[event] = out.trim()
        }
      }
    }
    assert.deepEqual(seen, {
      SessionStart: "ARGS hook-context",
      PreCompact: "ARGS hook-precompact",
      UserPromptSubmit: "ARGS hook-prompt",
      PreToolUse: "ARGS hook-tool",
      PostToolUse: "ARGS hook-tool-after",
      SessionEnd: "ARGS hook-session-end",
    })
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})

test("hookArgs passes deja's hooks and nothing else", () => {
  assert.deepEqual(hookArgs([]), ["hook-prompt", "--plain"])
  assert.deepEqual(hookArgs(["hook-tool"]), ["hook-tool"])
  assert.deepEqual(hookArgs(["index"]), ["hook-prompt", "--plain"])
})
