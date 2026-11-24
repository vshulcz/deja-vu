import { test } from "node:test"
import assert from "node:assert/strict"

import { mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"

import { DejaPlugin } from "../index.js"
import { settleRecall, trailingRecall, userTurns, v1Messages } from "../lib.js"
import serverPlugin from "../server.js"

// A stand-in deja that answers a different pointer on every hook-prompt call
// and a different digest on every hook-context call, as deja can across turns
// or copies when nothing deduplicates it.
function stand() {
  const dir = mkdtempSync(join(tmpdir(), "deja-oc-pileup-"))
  const bin = join(dir, "deja")
  const calls = join(dir, "calls")
  writeFileSync(
    bin,
    `#!/bin/sh\nif [ "$1" = version ]; then echo 0.0.0; exit 0; fi\nif [ "$1" = warmup-status ]; then exit 0; fi\nin=$(cat)\nprintf '%s\\n' "$1" >> ${calls}\n` +
      `if [ "$1" = hook-context ]; then n=$(grep -c '^hook-context' ${calls}); if [ $((n % 2)) = 1 ]; then c=CTX-A; else c=CTX-B; fi; ` +
      `printf '%s\\n' '{"hookSpecificOutput":{"additionalContext":"<deja-recall>\\nRecalled context.\\n'$c'\\n</deja-recall>"}}'; fi\n` +
      `if [ "$1" = hook-prompt ]; then n=$(grep -c '^hook-prompt' ${calls}); if [ $((n % 2)) = 1 ]; then p=POINTER-A; else p=POINTER-B; fi; ` +
      `printf '%s\\n' '{"hookSpecificOutput":{"additionalContext":"<deja-recall>\\nRecalled history.\\n'$p'\\n</deja-recall>"}}'; fi\n`,
    { mode: 0o755 },
  )
  const asked = () => readFileSync(calls, "utf8").split("\n").filter((l) => l === "hook-prompt").length
  const ctxAsked = () => {
    try {
      return readFileSync(calls, "utf8").split("\n").filter((l) => l === "hook-context").length
    } catch {
      return 0
    }
  }
  return { dir, bin, asked, ctxAsked }
}

async function pluginHooks(dir, bin) {
  const prevConfig = process.env.XDG_CONFIG_HOME
  const prevCache = process.env.XDG_CACHE_HOME
  process.env.XDG_CONFIG_HOME = dir
  process.env.XDG_CACHE_HOME = join(dir, ".cache")
  try {
    return await DejaPlugin({ client: {}, directory: dir }, { bin })
  } finally {
    if (prevConfig === undefined) delete process.env.XDG_CONFIG_HOME
    else process.env.XDG_CONFIG_HOME = prevConfig
    if (prevCache === undefined) delete process.env.XDG_CACHE_HOME
    else process.env.XDG_CACHE_HOME = prevCache
  }
}

async function plugin(dir, bin) {
  const hooks = await pluginHooks(dir, bin)
  return (messages) => hooks["experimental.chat.messages.transform"]({ sessionID: "ses_P" }, { messages })
}

const blocks = (text) => text.split("<deja-recall>").length - 1

// A host that keeps the message between model calls, and a 2.x message with no
// id: the block already appended changed the key, recall was asked again on
// every call and each answer appended (#5682).
test("a kept message with no id carries one block over six calls", async () => {
  const { dir, bin, asked } = stand()
  const call = await plugin(dir, bin)
  const raw = [{ role: "user", content: [{ type: "text", text: "why does the uploader retry so fast?" }] }]
  for (let i = 0; i < 6; i++) await call(v1Messages(raw, "ses_P"))
  assert.equal(blocks(raw[0].content[0].text), 1, raw[0].content[0].text)
  assert.equal(asked(), 1)
})

test("two copies of the plugin leave one block, the first one's", async () => {
  const { dir, bin, asked } = stand()
  const a = await plugin(dir, bin)
  const b = await plugin(dir, bin)
  const msgs = [{ info: { id: "m7", role: "user", sessionID: "ses_P" }, parts: [{ type: "text", text: "what did we settle on the importer?" }] }]
  for (let i = 0; i < 3; i++) {
    await a(msgs)
    await b(msgs)
  }
  const text = msgs[0].parts[0].text
  assert.equal(blocks(text), 1, text)
  assert.ok(text.includes("POINTER-A"), text)
  assert.equal(asked(), 1)
})

test("a pile from before collapses to its first block", () => {
  let text = "how is the cache keyed?"
  for (let i = 0; i < 6; i++) text += `\n\n<deja-recall>\nPOINTER-${i % 2 ? "B" : "A"}\n</deja-recall>`
  const tail = { text }
  settleRecall(tail, "")
  assert.equal(tail.text, "how is the cache keyed?\n\n<deja-recall>\nPOINTER-A\n</deja-recall>")
  const [turn] = userTurns([{ info: { id: "x", role: "user" }, parts: [{ type: "text", text }] }])
  assert.equal(turn.prompt, "how is the cache keyed?")
})

test("a block the user quoted in the middle of the prompt is theirs", () => {
  const text = "what does this mean?\n<deja-recall>\nquoted\n</deja-recall>\nexplain it"
  assert.deepEqual(trailingRecall(text), { body: text, blocks: [] })
  const tail = { text }
  settleRecall(tail, "<deja-recall>\nours\n</deja-recall>")
  assert.equal(tail.text, text + "\n\n<deja-recall>\nours\n</deja-recall>")
})

test("a prompt ending with whitespace settles to identical bytes across fresh, kept, and rebuilt calls", () => {
  const extra = "<deja-recall>\nPOINTER-A\n</deja-recall>"
  const want = "fix retry\n\n<deja-recall>\nPOINTER-A\n</deja-recall>"

  // Untouched prompt without recall must stay verbatim when extra is empty.
  const untouched = { text: "fix retry\n" }
  settleRecall(untouched, "")
  assert.equal(untouched.text, "fix retry\n")

  // Call 1 (fresh prompt ending with newline), Call 2 (kept message), Call 3 (rebuilt from DB).
  const fresh = { text: "fix retry\n" }
  settleRecall(fresh, extra)
  assert.equal(fresh.text, want)

  settleRecall(fresh, extra)
  assert.equal(fresh.text, want)

  const rebuilt = { text: "fix retry\n" }
  settleRecall(rebuilt, extra)
  assert.equal(rebuilt.text, want)
})

test("system transform leaves one digest block across reused system arrays and dual plugin copies", async () => {
  const sA = stand()
  const sB = stand()
  const hA = await pluginHooks(sA.dir, sA.bin)
  const hB = await pluginHooks(sB.dir, sB.bin)

  // 1. Reused system array with trailing newline + second independent plugin copy.
  const sys = { system: ["You are OpenCode.\n"] }
  await hA["experimental.chat.system.transform"]({ sessionID: "ses_S1" }, sys)
  await hA["experimental.chat.system.transform"]({ sessionID: "ses_S1" }, sys)
  await hB["experimental.chat.system.transform"]({ sessionID: "ses_S1" }, sys)
  assert.equal(sys.system[0], "You are OpenCode.\n\n<deja-recall>\nRecalled context.\nCTX-A\n</deja-recall>")
  assert.equal(blocks(sys.system[0]), 1)
  assert.equal(sA.ctxAsked(), 1)
  assert.equal(sB.ctxAsked(), 0)

  // 2. Empty system array [] reused across repeated calls and second copy.
  const sysEmpty = { system: [] }
  await hA["experimental.chat.system.transform"]({ sessionID: "ses_S2" }, sysEmpty)
  await hA["experimental.chat.system.transform"]({ sessionID: "ses_S2" }, sysEmpty)
  await hB["experimental.chat.system.transform"]({ sessionID: "ses_S2" }, sysEmpty)
  assert.equal(sysEmpty.system.length, 1)
  assert.equal(sysEmpty.system[0], "<deja-recall>\nRecalled context.\nCTX-B\n</deja-recall>")
  assert.equal(sA.ctxAsked(), 2)
  assert.equal(sB.ctxAsked(), 0)

  // 3. server.js wrapper against reused 2.x event.system array.
  const prevConfig = process.env.XDG_CONFIG_HOME
  const prevCache = process.env.XDG_CACHE_HOME
  process.env.XDG_CONFIG_HOME = sA.dir
  process.env.XDG_CACHE_HOME = join(sA.dir, ".cache")
  try {
    const contextHooks = []
    await serverPlugin.setup({
      location: { directory: sA.dir },
      options: { bin: sA.bin },
      tool: { async transform() {}, async hook() {} },
      session: {
        async hook(name, fn) {
          if (name === "context") contextHooks.push(fn)
        },
      },
    })
    const ev = { sessionID: "ses_S3", system: [{ type: "text", text: "You are OpenCode.\n" }], messages: [] }
    for (const fn of contextHooks) await fn(ev)
    for (const fn of contextHooks) await fn(ev)
    assert.equal(ev.system.length, 1)
    assert.equal(ev.system[0].text, "You are OpenCode.\n\n<deja-recall>\nRecalled context.\nCTX-A\n</deja-recall>")
  } finally {
    if (prevConfig === undefined) delete process.env.XDG_CONFIG_HOME
    else process.env.XDG_CONFIG_HOME = prevConfig
    if (prevCache === undefined) delete process.env.XDG_CACHE_HOME
    else process.env.XDG_CACHE_HOME = prevCache
  }

  // 4. Unframed digest suffix collision guard ("assistant" inside "You are a codebase assistant").
  assert.deepEqual(trailingRecall("You are a codebase assistant", "assistant", true), {
    body: "You are a codebase assistant",
    blocks: [],
  })
  const unframed = { text: "You are a codebase assistant" }
  const firstPass = trailingRecall(unframed.text, "assistant", true)
  const firstOut = (firstPass.body.trimEnd() ? firstPass.body.trimEnd() + "\n\n" : "") + "assistant"
  assert.equal(firstOut, "You are a codebase assistant\n\nassistant")
  const secondPass = trailingRecall(firstOut, "assistant", true)
  assert.deepEqual(secondPass, { body: "You are a codebase assistant", blocks: ["assistant"] })
})
