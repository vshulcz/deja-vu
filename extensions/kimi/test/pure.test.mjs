import { test } from "node:test"
import assert from "node:assert/strict"
import { mkdtempSync, mkdirSync, writeFileSync, rmSync, chmodSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"

import {
  installerHookMarker,
  installerHookPresent,
  installerMcpPresent,
  installerOwns,
  kimiHome,
  resolveDeja,
  wellKnown,
} from "../lib.mjs"

test("kimiHome follows KIMI_CODE_HOME, which is what Kimi hands its plugins", () => {
  assert.equal(kimiHome({ KIMI_CODE_HOME: "/tmp/k" }, "/home/x"), "/tmp/k")
  assert.equal(kimiHome({}, "/home/x"), join("/home/x", ".kimi-code"))
})

test("installerHookPresent finds the marker deja install kimi-auto writes", () => {
  const written = `${installerHookMarker}\n[[hooks]]\nevent = "UserPromptSubmit"\n`
  assert.equal(installerHookPresent(written), true)
  assert.equal(installerHookPresent('[[hooks]]\nevent = "Stop"\n'), false)
  assert.equal(installerHookPresent(""), false)
  assert.equal(installerHookPresent(undefined), false)
})

test("installerMcpPresent reads mcp.json, and a broken one is not evidence", () => {
  assert.equal(installerMcpPresent('{"mcpServers":{"deja":{"command":"deja"}}}'), true)
  assert.equal(installerMcpPresent('{"mcpServers":{"other":{}}}'), false)
  assert.equal(installerMcpPresent("{"), false)
  assert.equal(installerMcpPresent(""), false)
})

test("installerOwns reads the real files under the config home", () => {
  const dir = mkdtempSync(join(tmpdir(), "deja-kimi-"))
  try {
    const env = { KIMI_CODE_HOME: dir }
    assert.equal(installerOwns("hook", env), false)
    assert.equal(installerOwns("mcp", env), false)

    writeFileSync(join(dir, "config.toml"), `${installerHookMarker}\n[[hooks]]\n`)
    writeFileSync(join(dir, "mcp.json"), '{"mcpServers":{"deja":{"command":"deja"}}}')
    assert.equal(installerOwns("hook", env), true)
    assert.equal(installerOwns("mcp", env), true)
    assert.equal(installerOwns("something-else", env), false)
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})

test("resolveDeja prefers DEJA_BIN, then the user's own install", () => {
  const seen = []
  const exists = (path) => {
    seen.push(path)
    return path.endsWith(join(".local", "bin", "deja"))
  }
  assert.equal(resolveDeja({ DEJA_BIN: "/opt/deja" }, "/home/x", () => true), "/opt/deja")
  assert.equal(resolveDeja({}, "/home/x", exists), join("/home/x", ".local", "bin", "deja"))
  // A DEJA_BIN that is not there must not win over a deja that is.
  assert.equal(resolveDeja({ DEJA_BIN: "/nope/deja" }, "/home/x", exists), join("/home/x", ".local", "bin", "deja"))
})

test("resolveDeja falls back to the bare name so the error names deja", () => {
  assert.equal(resolveDeja({}, "/home/x", () => false), process.platform === "win32" ? "deja.exe" : "deja")
})

test("wellKnown covers where an install actually lands", () => {
  const unix = wellKnown("/home/x", "linux")
  assert.ok(unix.includes("/usr/local/bin/deja"))
  assert.ok(unix.includes("/opt/homebrew/bin/deja"))
  assert.ok(unix[0].startsWith("/home/x"))
  const win = wellKnown("C:\\Users\\x", "win32")
  assert.ok(win.every((p) => p.endsWith("deja.exe")))
})

// The hook is the whole integration: Kimi appends a UserPromptSubmit hook's
// stdout to the turn. This runs the real script the way Kimi runs it.
test("the hook stands down when the installer already wired the same recall", async () => {
  const { execFileSync } = await import("node:child_process")
  const dir = mkdtempSync(join(tmpdir(), "deja-kimi-hook-"))
  try {
    const fake = join(dir, "deja")
    writeFileSync(fake, "#!/bin/sh\necho RECALLED\n")
    chmodSync(fake, 0o755)
    const script = new URL("../hooks/recall.mjs", import.meta.url).pathname
    const payload = JSON.stringify({ hook_event_name: "UserPromptSubmit", prompt: "anything", cwd: dir })
    const call = () =>
      execFileSync(process.execPath, [script], {
        input: payload,
        encoding: "utf8",
        env: { ...process.env, DEJA_BIN: fake, KIMI_CODE_HOME: dir },
      })

    assert.match(call(), /RECALLED/)

    mkdirSync(dir, { recursive: true })
    writeFileSync(join(dir, "config.toml"), `${installerHookMarker}\n[[hooks]]\n`)
    assert.equal(call().trim(), "")
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})
