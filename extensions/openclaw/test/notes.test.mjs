import { test } from "node:test"
import assert from "node:assert/strict"
import { chmodSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"

import { deliversNotes, directTurns, ownerSender, receiptBoard, receiptLine, splitHookReply, withReceipt } from "../lib.mjs"

const RECEIPT = 'deja-vu — you have been here: "how should the zorbit queue drain on deploy" (Oct 4 · via: zorbit, queue, decide)'
const LINE = '↺ you\'ve been here: "how should the zorbit queue drain on deploy" · Oct 4 · deja'
const CONTEXT = "<deja-recall>\ndrain with --grace 40\n</deja-recall>"
const OWNER = { channels: { telegram: { allowFrom: ["4242"] } } }

test("the JSON reply splits into the model's text and the person's line", () => {
  const reply = JSON.stringify({ systemMessage: RECEIPT, hookSpecificOutput: { hookEventName: "UserPromptSubmit", additionalContext: CONTEXT } })
  assert.deepEqual(splitHookReply(reply), { context: CONTEXT, notes: RECEIPT })
  assert.deepEqual(splitHookReply("warning: slow disk\n" + reply), { context: CONTEXT, notes: RECEIPT })
  assert.deepEqual(splitHookReply(CONTEXT), { context: CONTEXT, notes: "" })
  assert.deepEqual(splitHookReply(""), { context: "", notes: "" })
})

test("the receipt becomes one short line without the brand or the via terms", () => {
  assert.equal(receiptLine(RECEIPT), LINE)
  assert.equal(
    receiptLine('deja-vu — this was done on another machine: "rotate the \\"api\\" key" (3d ago)'),
    '↺ done on another machine: "rotate the \\"api\\" key" · 3d ago · deja',
  )
  // Anything that is not the receipt is not shown on a channel.
  assert.equal(receiptLine("deja recalled 3 prior sessions from this project"), "")
  assert.equal(receiptLine("docs: vshulcz.github.io/deja-vu · issues: github.com/vshulcz/deja-vu/issues"), "")
})

test("only a channel that delivers its reply can carry the receipt", () => {
  for (const channel of ["telegram", "slack", "Discord"]) assert.equal(deliversNotes(channel), true, channel)
  for (const channel of ["webchat", "", undefined]) assert.equal(deliversNotes(channel), false, String(channel))
})

test("the owner is ownerAllowFrom, else a channel allowlist of exactly one person", () => {
  assert.equal(ownerSender({ commands: { ownerAllowFrom: ["telegram:4242"] } }, "telegram", "4242"), true)
  assert.equal(ownerSender({ commands: { ownerAllowFrom: ["telegram:4242"] }, ...OWNER }, "telegram", "7"), false)
  assert.equal(ownerSender(OWNER, "telegram", "4242"), true)
  assert.equal(ownerSender(OWNER, "telegram", "7"), false)
  // Several people, a wildcard or no list: not known to be the owner.
  assert.equal(ownerSender({ channels: { telegram: { allowFrom: ["4242", "7"] } } }, "telegram", "4242"), false)
  assert.equal(ownerSender({ channels: { telegram: { allowFrom: ["*"] } } }, "telegram", "4242"), false)
  assert.equal(ownerSender({}, "telegram", "4242"), false)
})

test("a turn counts as 1:1 only when before_dispatch said so, recently, for that sender", () => {
  let clock = 0
  const turns = directTurns({ ttlMs: 1000, now: () => clock })
  turns.heard({ sessionKey: "k", channel: "telegram", senderId: "4242", isGroup: false })
  assert.equal(turns.direct("k", "telegram", "4242"), true)
  assert.equal(turns.direct("k", "telegram", "7"), false)
  assert.equal(turns.direct("k", "slack", "4242"), false)
  assert.equal(turns.direct("other", "telegram", "4242"), false)
  turns.heard({ sessionKey: "g", channel: "telegram", senderId: "4242", isGroup: true })
  assert.equal(turns.direct("g", "telegram", "4242"), false)
  // A host that does not say isGroup is not taken for a 1:1 chat.
  turns.heard({ sessionKey: "u", channel: "telegram", senderId: "4242" })
  assert.equal(turns.direct("u", "telegram", "4242"), false)
  clock = 5000
  assert.equal(turns.direct("k", "telegram", "4242"), false)
})

test("each turn sets the board, a silent turn clears it, and a reply takes it once", () => {
  const board = receiptBoard()
  board.set("k", "telegram", LINE)
  board.set("k", "telegram", "")
  assert.equal(board.take("k", "telegram"), "")
  board.set("k", "telegram", LINE)
  assert.equal(board.take("k", "slack"), "")
  assert.equal(board.take("k", "telegram"), LINE)
  assert.equal(board.take("k", "telegram"), "")
})

test("the receipt goes under the reply as one italic context block", () => {
  const out = withReceipt({ text: "Use --grace 40.", presentation: { blocks: [{ type: "divider" }] } }, LINE)
  assert.equal(out.text, "Use --grace 40.")
  assert.deepEqual(out.presentation.blocks, [{ type: "divider" }, { type: "context", text: "_" + LINE + "_" }])
  const none = { text: "x" }
  assert.equal(withReceipt(none, ""), none)
})

// The plugin as OpenClaw drives it: before_dispatch, the prompt hook, then the
// reply on its way out. A stub deja answers both forms and records its calls.
async function drive({ channel, isGroup, senderId = "4242", pluginConfig = {}, hostConfig = OWNER, reply = true }) {
  const dir = mkdtempSync(join(tmpdir(), "openclaw-notes-"))
  const calls = join(dir, "calls")
  const stub = join(dir, "deja")
  const json = JSON.stringify({ systemMessage: RECEIPT, hookSpecificOutput: { hookEventName: "UserPromptSubmit", additionalContext: CONTEXT } })
  writeFileSync(
    stub,
    "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + calls + "\ncase \"$1\" in hook-*) in=$(cat) ;; esac\n" +
      // Only the zorbit question is in the history; anything else is silence.
      'case "$in" in *zorbit*) ;; *) [ "$1" = hook-prompt ] && exit 0 ;; esac\n' +
      'case "$*" in\n' +
      "  --version) echo 0.22.1 ;;\n" +
      "  'hook-prompt --plain') printf '%s' '" + CONTEXT.replace(/\n/g, "\\n") + "' | sed 's/\\\\n/\\n/g' ;;\n" +
      "  hook-prompt) printf '%s\\n' '" + json + "' ;;\n" +
      "esac\n",
  )
  chmodSync(stub, 0o755)
  const prev = process.env.OPENCLAW_STATE_DIR
  process.env.OPENCLAW_STATE_DIR = dir
  const hooks = {}
  const { default: plugin } = await import("../index.mjs?" + Math.random())
  plugin.register({
    id: "deja-vu",
    pluginConfig: { bin: stub, tools: false, ...pluginConfig },
    config: { ...hostConfig, plugins: { entries: { "deja-vu": { hooks: { allowConversationAccess: true } } } } },
    on: (name, fn) => (hooks[name] = fn),
    logger: { warn() {} },
  })
  if (prev === undefined) delete process.env.OPENCLAW_STATE_DIR
  else process.env.OPENCLAW_STATE_DIR = prev
  const key = isGroup ? "agent:main:telegram:group:-100" : "agent:main:main"
  await hooks.before_dispatch?.({ sessionKey: key, channel, senderId, isGroup }, { sessionKey: key, channelId: channel, senderId })
  const ctx = { sessionKey: key, sessionId: "s-1", workspaceDir: dir, channel, messageProvider: channel, senderId, trigger: "user" }
  const built = await hooks.before_prompt_build({ prompt: "what did we decide about draining the zorbit queue", messages: [] }, ctx)
  const sent =
    reply && hooks.reply_payload_sending
      ? await hooks.reply_payload_sending({ kind: "final", channel, sessionKey: key, payload: { text: "Use --grace 40." } }, {})
      : undefined
  return { built, sent, calls: readFileSync(calls, "utf8"), hooks, key }
}

const promptCalls = (calls) => calls.split("\n").filter((c) => c.startsWith("hook-prompt"))

test("a 1:1 chat with the owner shows the receipt under the reply and gives the model what --plain prints", async () => {
  const { built, sent, calls } = await drive({ channel: "telegram", isGroup: false })
  assert.equal(built.prependContext, CONTEXT)
  assert.ok(sent && sent.payload, "the reply went out without the receipt")
  assert.equal(sent.payload.text, "Use --grace 40.")
  assert.deepEqual(sent.payload.presentation.blocks, [{ type: "context", text: "_" + LINE + "_" }])
  assert.deepEqual(promptCalls(calls), ["hook-prompt"])
})

test("a group chat, someone else's DM, the TUI and receipt:false all keep --plain and leave the reply alone", async () => {
  const cases = [
    { channel: "telegram", isGroup: true },
    { channel: "telegram", isGroup: false, senderId: "7" },
    { channel: "webchat", isGroup: false },
    { channel: "telegram", isGroup: false, pluginConfig: { receipt: false } },
  ]
  for (const c of cases) {
    const { built, sent, calls } = await drive(c)
    assert.equal(built.prependContext, CONTEXT, JSON.stringify(c))
    assert.equal(sent, undefined, JSON.stringify(c))
    assert.deepEqual(promptCalls(calls), ["hook-prompt --plain"], JSON.stringify(c))
  }
})

test("the session-start digest stays --plain on a channel, so its notes are not spent there", async () => {
  const { calls } = await drive({ channel: "telegram", isGroup: false })
  assert.doesNotMatch(calls, /^hook-context$/m)
})

test("a reply that never goes out leaves nothing for the next turn's reply", async () => {
  const { hooks, key } = await drive({ channel: "telegram", isGroup: false, reply: false })
  // The next turn finds nothing in the history: its reply carries no receipt.
  await hooks.before_dispatch({ sessionKey: key, channel: "telegram", senderId: "4242", isGroup: false }, {})
  await hooks.before_prompt_build({ prompt: "hello", messages: [] }, { sessionKey: key, channel: "telegram", senderId: "4242", trigger: "user" })
  const sent = await hooks.reply_payload_sending({ kind: "final", channel: "telegram", sessionKey: key, payload: { text: "hi" } }, {})
  assert.equal(sent, undefined)
})
