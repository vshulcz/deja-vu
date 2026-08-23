import { test } from "node:test"
import assert from "node:assert/strict"

import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"

import { clampLimit, cliPluginPath, contextText, lastUserText } from "../lib.js"
import { DejaPlugin } from "../index.js"

test("contextText reads the hook shape deja prints", () => {
  const raw = JSON.stringify({
    systemMessage: "deja: recalled 3 prior sessions",
    hookSpecificOutput: { additionalContext: "past work" },
  })
  assert.deepEqual(contextText(raw), { context: "past work", receipt: "deja: recalled 3 prior sessions" })
})

test("contextText falls back to bare text", () => {
  assert.deepEqual(contextText("  past work  "), { context: "past work", receipt: "" })
  assert.deepEqual(contextText(""), { context: "", receipt: "" })
})

test("lastUserText joins the newest user message, ignoring the assistant's", () => {
  const messages = [
    { info: { role: "user" }, parts: [{ type: "text", text: "old" }] },
    { info: { role: "assistant" }, parts: [{ type: "text", text: "reply" }] },
    { info: { role: "user" }, parts: [{ type: "text", text: "why" }, { type: "text", text: "this" }] },
  ]
  const { prompt, parts } = lastUserText(messages)
  assert.equal(prompt, "why\nthis")
  assert.equal(parts.length, 2)
})

test("lastUserText is empty when there is nothing to rank against", () => {
  assert.equal(lastUserText([]).prompt, "")
  assert.equal(lastUserText(undefined).prompt, "")
  assert.equal(lastUserText([{ info: { role: "user" }, parts: [{ type: "file" }] }]).prompt, "")
})

test("clampLimit keeps the window small", () => {
  assert.equal(clampLimit(undefined), 5)
  assert.equal(clampLimit("3"), 3)
  assert.equal(clampLimit(0), 1)
  assert.equal(clampLimit(500), 20)
  assert.equal(clampLimit(NaN), 5)
})

test("cliPluginPath follows the installer's own config home", () => {
  assert.equal(
    cliPluginPath({ XDG_CONFIG_HOME: "/cfg" }, "/home/x"),
    join("/cfg", "opencode", "plugins", "deja.js"),
  )
  assert.equal(
    cliPluginPath({}, "/home/x"),
    join("/home/x", ".config", "opencode", "plugins", "deja.js"),
  )
})

// opencode resolves both installs into one plugin list — verified with
// `opencode debug config` against a config home holding both — and each pushed
// the same recall onto the system prompt and raised the same toast.
test("the plugin stands down when the installer's own plugin is on disk", async () => {
  const dir = mkdtempSync(join(tmpdir(), "deja-oc-"))
  mkdirSync(join(dir, "opencode", "plugins"), { recursive: true })
  writeFileSync(join(dir, "opencode", "plugins", "deja.js"), "// installed by deja\n")

  const client = { tui: { showToast: async () => {} } }
  const env = process.env.XDG_CONFIG_HOME
  process.env.XDG_CONFIG_HOME = dir
  try {
    const hooks = await DejaPlugin({ client, directory: dir })
    assert.equal(hooks["experimental.chat.system.transform"], undefined)
    assert.equal(hooks["experimental.chat.messages.transform"], undefined)
  } finally {
    if (env === undefined) delete process.env.XDG_CONFIG_HOME
    else process.env.XDG_CONFIG_HOME = env
    rmSync(dir, { recursive: true, force: true })
  }
})

test("without it the recall hooks are installed", async () => {
  const dir = mkdtempSync(join(tmpdir(), "deja-oc-"))
  const client = { tui: { showToast: async () => {} } }
  const env = process.env.XDG_CONFIG_HOME
  process.env.XDG_CONFIG_HOME = dir
  try {
    const hooks = await DejaPlugin({ client, directory: dir })
    assert.equal(typeof hooks["experimental.chat.system.transform"], "function")
    assert.equal(typeof hooks["experimental.chat.messages.transform"], "function")
  } finally {
    if (env === undefined) delete process.env.XDG_CONFIG_HOME
    else process.env.XDG_CONFIG_HOME = env
    rmSync(dir, { recursive: true, force: true })
  }
})
