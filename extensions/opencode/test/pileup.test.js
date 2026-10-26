import { test } from "node:test"
import assert from "node:assert/strict"

import { mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"

import { DejaPlugin } from "../index.js"
import { settleRecall, trailingRecall, userTurns, v1Messages } from "../lib.js"

// A stand-in deja that answers a different pointer on every hook-prompt call,
// as deja does once it has shown the first one.
function stand() {
  const dir = mkdtempSync(join(tmpdir(), "deja-oc-pileup-"))
  const bin = join(dir, "deja")
  const calls = join(dir, "calls")
  writeFileSync(
    bin,
    `#!/bin/sh\nif [ "$1" = version ]; then echo 0.0.0; exit 0; fi\nif [ "$1" = warmup-status ]; then exit 0; fi\nin=$(cat)\nprintf '%s\\n' "$1" >> ${calls}\n` +
      `if [ "$1" = hook-prompt ]; then n=$(grep -c '^hook-prompt' ${calls}); if [ $((n % 2)) = 1 ]; then p=POINTER-A; else p=POINTER-B; fi; ` +
      `printf '%s\\n' '{"hookSpecificOutput":{"additionalContext":"<deja-recall>\\nRecalled history.\\n'$p'\\n</deja-recall>"}}'; fi\n`,
    { mode: 0o755 },
  )
  const asked = () => readFileSync(calls, "utf8").split("\n").filter((l) => l === "hook-prompt").length
  return { dir, bin, asked }
}

async function plugin(dir, bin) {
  const previous = process.env.XDG_CONFIG_HOME
  process.env.XDG_CONFIG_HOME = dir
  try {
    const hooks = await DejaPlugin({ client: {}, directory: dir }, { bin })
    return (messages) => hooks["experimental.chat.messages.transform"]({ sessionID: "ses_P" }, { messages })
  } finally {
    if (previous === undefined) delete process.env.XDG_CONFIG_HOME
    else process.env.XDG_CONFIG_HOME = previous
  }
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
