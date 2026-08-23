import { test } from "node:test"
import assert from "node:assert/strict"

import { clampLimit, contextText, lastUserText } from "../lib.js"

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
