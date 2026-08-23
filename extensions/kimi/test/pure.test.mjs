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
  const written = `${installerHookMarker}\n[[hooks]]\nevent = "UserPromptSubmit"\ncommand = "deja hook-prompt --plain"\n`
  assert.equal(installerHookPresent(written), true)
  assert.equal(installerHookPresent('[[hooks]]\nevent = "Stop"\n'), false)
  assert.equal(installerHookPresent(""), false)
  assert.equal(installerHookPresent(undefined), false)
})

// An older deja wired kimi through SessionStart, whose output Kimi runs and
// then ignores. That block still carries the marker, and standing down for it
// would leave the user with no recall from either half.
test("a stale installer block does not count as wiring", () => {
  const stale = `${installerHookMarker}\n[[hooks]]\nevent = "SessionStart"\ncommand = "deja hook-context"\ntimeout = 10\n`
  assert.equal(installerHookPresent(stale), false)

  // A live block followed by the user's own rule is still a live block, and a
  // hook-prompt call in *their* rule is not ours to stand down for.
  const followed = `${installerHookMarker}\n[[hooks]]\ncommand = "deja hook-prompt --plain"\n\n[[hooks]]\nevent = "Stop"\n`
  assert.equal(installerHookPresent(followed), true)
  const theirs = `${installerHookMarker}\n[[hooks]]\nevent = "SessionStart"\ncommand = "deja hook-context"\n\n[[hooks]]\ncommand = "deja hook-prompt --plain"\n`
  assert.equal(installerHookPresent(theirs), false)
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

    writeFileSync(join(dir, "config.toml"), `${installerHookMarker}\n[[hooks]]\ncommand = "deja hook-prompt --plain"\n`)
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
    writeFileSync(join(dir, "config.toml"), `${installerHookMarker}\n[[hooks]]\ncommand = "deja hook-prompt --plain"\n`)
    assert.equal(call().trim(), "")
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})

// The MCP launcher is the plugin's other entry point: Kimi starts it, and
// whatever it does with stdio is the server the agent talks to.
test("the MCP launcher stands down when the installer already declared the server", async () => {
  const { execFileSync } = await import("node:child_process")
  const dir = mkdtempSync(join(tmpdir(), "deja-kimi-mcp-"))
  try {
    const fake = join(dir, "deja")
    writeFileSync(fake, "#!/bin/sh\necho STARTED\nexit 7\n")
    chmodSync(fake, 0o755)
    const script = new URL("../bin/deja-mcp.mjs", import.meta.url).pathname
    const call = () => {
      try {
        return {
          out: execFileSync(process.execPath, [script], {
            encoding: "utf8",
            stdio: ["ignore", "pipe", "pipe"],
            env: { ...process.env, DEJA_BIN: fake, KIMI_CODE_HOME: dir },
          }),
          code: 0,
        }
      } catch (err) {
        return { out: String(err.stdout ?? ""), code: err.status }
      }
    }

    // Nothing wired: the launcher runs deja and hands its exit code back, so a
    // server that dies is reported as dead rather than as an empty success.
    const ran = call()
    assert.match(ran.out, /STARTED/)
    assert.equal(ran.code, 7)

    writeFileSync(join(dir, "mcp.json"), '{"mcpServers":{"deja":{"command":"deja"}}}')
    const stoodDown = call()
    assert.doesNotMatch(stoodDown.out, /STARTED/)
    assert.equal(stoodDown.code, 0)
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})
