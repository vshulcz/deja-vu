import { test } from "node:test"
import assert from "node:assert/strict"
import { chmodSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"

import {
  wholeReplies,
  conversationOf,
  deliversNotes,
  directSessionKey,
  directTurn,
  escapeMarkdown,
  ownerSender,
  receiptAllowed,
  receiptLine,
  replyTakes,
  splitHookReply,
  withReceipt,
} from "../lib.mjs"

const RECEIPT = 'deja-vu — you have been here: "how should the zorbit queue drain on deploy" (Oct 4 · via: zorbit, queue, decide)'
const LINE = '↺ you\'ve been here: "how should the zorbit queue drain on deploy" · Oct 4 · deja'
const CONTEXT = "<deja-recall>\ndrain with --grace 40\n</deja-recall>"
const OWNER = { channels: { telegram: { allowFrom: ["4242"], streaming: { mode: "off" } } } }

test("the JSON reply splits into the model's text and the person's line", () => {
  const reply = JSON.stringify({ systemMessage: RECEIPT, hookSpecificOutput: { hookEventName: "UserPromptSubmit", additionalContext: CONTEXT } })
  assert.deepEqual(splitHookReply(reply), { context: CONTEXT, notes: RECEIPT })
  assert.deepEqual(splitHookReply(CONTEXT), { context: CONTEXT, notes: "" })
  assert.deepEqual(splitHookReply(""), { context: "", notes: "" })
})

test("a stray stdout line stays with the model's text, where --plain would print it", () => {
  const reply = JSON.stringify({ systemMessage: RECEIPT, hookSpecificOutput: { additionalContext: CONTEXT } })
  assert.deepEqual(splitHookReply("deja: index rebuilt\n" + reply), { context: "deja: index rebuilt\n" + CONTEXT, notes: RECEIPT })
  // Its plain counterpart, for the same stray line.
  assert.equal(splitHookReply("deja: index rebuilt\n" + CONTEXT).context, "deja: index rebuilt\n" + CONTEXT)
})

test("the receipt becomes one short line without the brand or the via terms", () => {
  assert.equal(receiptLine(RECEIPT), LINE)
  // #4994 drops the colon after "via".
  assert.equal(receiptLine('deja-vu — you have been here: "how should the zorbit queue drain on deploy" (Oct 4 · via zorbit)'), LINE)
  assert.equal(receiptLine('deja-vu — you have been here: "how should the zorbit queue drain on deploy" (Oct 4)'), LINE)
  assert.equal(
    receiptLine('deja-vu — this was done on another machine: "rotate the \\"api\\" key" (3d ago)'),
    '↺ done on another machine: "rotate the "api" key" · 3d ago · deja',
  )
  assert.equal(receiptLine("deja recalled 3 prior sessions from this project"), "")
  assert.equal(receiptLine("docs: vshulcz.github.io/deja-vu · issues: github.com/vshulcz/deja-vu/issues"), "")
})

test("markdown in the topic is escaped so it cannot end the italics", () => {
  assert.equal(escapeMarkdown("fix my_module *now* [x] `y`"), "fix my\\_module \\*now\\* \\[x\\] \\`y\\`")
  assert.equal(
    receiptLine('deja-vu — you have been here: "rename snake_case_names" (Oct 4 · via: rename)'),
    '↺ you\'ve been here: "rename snake\\_case\\_names" · Oct 4 · deja',
  )
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
  assert.equal(ownerSender({ channels: { telegram: { allowFrom: ["4242", "7"] } } }, "telegram", "4242"), false)
  assert.equal(ownerSender({ channels: { telegram: { allowFrom: ["*"] } } }, "telegram", "4242"), false)
  assert.equal(ownerSender({}, "telegram", "4242"), false)
})

test("a session key that names a group, a channel, a thread or a topic is never 1:1", () => {
  assert.equal(directSessionKey("agent:main:main"), true)
  assert.equal(directSessionKey("agent:main:telegram:direct:4242"), true)
  for (const key of ["agent:main:telegram:group:-100", "agent:main:discord:channel:1", "agent:main:slack:direct:u1:thread:9", "agent:main:telegram:group:-1:topic:2", ""]) {
    assert.equal(directSessionKey(key), false, key)
  }
  assert.equal(conversationOf("telegram:4242", "telegram"), "4242")
  assert.equal(conversationOf("4242", "telegram"), "4242")
})

const dmEvent = { sessionKey: "agent:main:main", channel: "telegram", senderId: "4242", isGroup: false }
const dmCtx = { sessionKey: "agent:main:main", channelId: "telegram", conversationId: "4242", senderId: "4242" }

test("a turn is 1:1 only when every signal agrees, and anything missing fails closed", () => {
  assert.equal(directTurn(dmEvent, dmCtx).direct, true)
  const notDirect = [
    [{ ...dmEvent, isGroup: undefined }, dmCtx],
    [{ ...dmEvent, isGroup: true }, dmCtx],
    // isGroup false, but the key is a group's: a channel that never set the group subject.
    [{ ...dmEvent, sessionKey: "agent:main:googlechat:group:spaces/a" }, { ...dmCtx, sessionKey: "agent:main:googlechat:group:spaces/a" }],
    [{ ...dmEvent, chatType: "group" }, dmCtx],
    [dmEvent, { ...dmCtx, conversationId: undefined }],
  ]
  for (const [e, c] of notDirect) assert.equal(directTurn(e, c).direct, false, JSON.stringify([e, c]))
})

test("the receipt is asked for only on the owner's 1:1 turn with a run id", () => {
  const turn = directTurn(dmEvent, dmCtx)
  const ctx = { sessionKey: "agent:main:main", senderId: "4242", runId: "r1" }
  assert.equal(receiptAllowed(turn, ctx, "telegram", OWNER), true)
  assert.equal(receiptAllowed(turn, { ...ctx, runId: undefined }, "telegram", OWNER), false)
  assert.equal(receiptAllowed(turn, { ...ctx, senderId: "7" }, "telegram", OWNER), false)
  assert.equal(receiptAllowed(turn, { ...ctx, chatType: "group" }, "telegram", OWNER), false)
  assert.equal(receiptAllowed(turn, { ...ctx, sessionKey: "agent:main:other" }, "telegram", OWNER), false)
  assert.equal(receiptAllowed(turn, ctx, "webchat", OWNER), false)
  assert.equal(receiptAllowed(turn, ctx, "telegram", {}), false)
  assert.equal(receiptAllowed(undefined, ctx, "telegram", OWNER), false)
})

test("only the final text reply of the same run, to the same conversation, takes the receipt", () => {
  const held = { ...directTurn(dmEvent, dmCtx), runId: "r1", line: LINE }
  const event = { kind: "final", channel: "telegram", sessionKey: "agent:main:main", runId: "r1", usageState: { chatType: "direct" }, payload: { text: "ok" } }
  const ctx = { channelId: "telegram", conversationId: "telegram:4242", senderId: "4242", runId: "r1" }
  assert.equal(replyTakes(held, event, ctx), true)
  // No chat type from the delivery is not a 1:1 chat.
  assert.equal(replyTakes(held, { ...event, usageState: undefined }, ctx), false)
  assert.equal(replyTakes(held, { ...event, usageState: {} }, ctx), false)
  assert.equal(replyTakes(held, { ...event, kind: "block" }, ctx), false)
  assert.equal(replyTakes(held, { ...event, runId: "r2" }, { ...ctx, runId: "r2" }), false)
  assert.equal(replyTakes(held, event, { ...ctx, conversationId: "telegram:7" }), false)
  assert.equal(replyTakes(held, event, { ...ctx, senderId: "7" }), false)
  assert.equal(replyTakes(held, { ...event, usageState: { chatType: "group" } }, ctx), false)
  assert.equal(replyTakes(held, { ...event, payload: { mediaUrl: "file:///a.png" } }, ctx), false)
  assert.equal(replyTakes(held, { ...event, payload: { text: "hi", audioAsVoice: true } }, ctx), false)
})

test("the receipt goes under the reply as one italic context block", () => {
  const out = withReceipt({ text: "Use --grace 40.", presentation: { blocks: [{ type: "divider" }] } }, LINE)
  assert.equal(out.text, "Use --grace 40.")
  assert.deepEqual(out.presentation.blocks, [{ type: "divider" }, { type: "context", text: "_" + LINE + "_" }])
  const none = { text: "x" }
  assert.equal(withReceipt(none, ""), none)
})

// The plugin as OpenClaw drives it, one turn at a time: before_dispatch for a
// channel message, the prompt hook, then the replies on their way out. A stub
// deja answers the zorbit question in both forms and records its calls.
async function plugin({ pluginConfig = {}, hostConfig = OWNER } = {}) {
  const dir = mkdtempSync(join(tmpdir(), "openclaw-notes-"))
  const calls = join(dir, "calls")
  const stub = join(dir, "deja")
  const json = JSON.stringify({ systemMessage: RECEIPT, hookSpecificOutput: { hookEventName: "UserPromptSubmit", additionalContext: CONTEXT } })
  writeFileSync(
    stub,
    "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + calls + "\ncase \"$1\" in hook-*) in=$(cat) ;; esac\n" +
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
  const { default: mod } = await import("../index.mjs?" + Math.random())
  mod.register({
    id: "deja-vu",
    pluginConfig: { bin: stub, tools: false, ...pluginConfig },
    config: { ...hostConfig, plugins: { entries: { "deja-vu": { hooks: { allowConversationAccess: true } } } } },
    on: (name, fn) => (hooks[name] = fn),
    logger: { warn() {} },
  })
  if (prev === undefined) delete process.env.OPENCLAW_STATE_DIR
  else process.env.OPENCLAW_STATE_DIR = prev
  let n = 0
  return {
    hooks,
    calls: () => readFileSync(calls, "utf8"),
    // One turn. dispatch: false is a run with no inbound message (a heartbeat).
    // chatType is what the delivery's usageState says; replyConversation is the
    // reply hook's conversation id; prompts is how many times the prompt hook
    // fires in the run.
    async turn({ key = "agent:main:main", channel = "telegram", sender = "4242", conversation = sender, isGroup = false, prompt = "what did we decide about draining the zorbit queue", trigger = "user", dispatch = true, kinds = ["final"], chatType = isGroup ? "group" : "direct", replyConversation = channel + ":" + conversation, prompts = 1 } = {}) {
      const runId = "run-" + ++n
      if (dispatch) {
        await hooks.before_dispatch({ sessionKey: key, channel, senderId: sender, isGroup }, { sessionKey: key, channelId: channel, conversationId: conversation, senderId: sender })
      }
      const ctx = { sessionKey: key, sessionId: "s", workspaceDir: dir, channel, messageProvider: channel, senderId: dispatch ? sender : undefined, trigger, runId }
      let built
      for (let i = 0; i < prompts; i++) built = (await hooks.before_prompt_build({ prompt, messages: [] }, ctx)) ?? built
      const sent = []
      for (const kind of kinds) {
        const out = hooks.reply_payload_sending
          ? await hooks.reply_payload_sending(
              { kind, channel, sessionKey: key, runId, usageState: chatType ? { chatType } : undefined, payload: { text: "Use --grace 40." } },
              { channelId: channel, conversationId: replyConversation, sessionKey: key, senderId: dispatch ? sender : undefined, runId },
            )
          : undefined
        sent.push(out)
      }
      return { built, sent }
    },
  }
}

const promptCalls = (calls) => calls.split("\n").filter((c) => c.startsWith("hook-prompt"))
const blocksOf = (out) => out && out.payload && out.payload.presentation && out.payload.presentation.blocks

test("a 1:1 chat with the owner shows the receipt under the reply and gives the model what --plain prints", async () => {
  const p = await plugin()
  const { built, sent } = await p.turn()
  assert.equal(built.prependContext, CONTEXT)
  assert.deepEqual(blocksOf(sent[0]), [{ type: "context", text: "_" + LINE + "_" }])
  assert.equal(sent[0].payload.text, "Use --grace 40.")
  assert.deepEqual(promptCalls(p.calls()), ["hook-prompt"])
})

test("a group chat, someone else's DM, the TUI and receipt:false keep --plain and leave the reply alone", async () => {
  const cases = [
    [{}, { key: "agent:main:telegram:group:-100", conversation: "-100", isGroup: true }],
    [{}, { sender: "7" }],
    [{}, { channel: "webchat" }],
    [{ pluginConfig: { receipt: false } }, {}],
  ]
  for (const [opts, turn] of cases) {
    const p = await plugin(opts)
    const { built, sent } = await p.turn(turn)
    assert.equal(built.prependContext, CONTEXT, JSON.stringify(turn))
    assert.equal(sent[0], undefined, JSON.stringify(turn))
    assert.deepEqual(promptCalls(p.calls()), ["hook-prompt --plain"], JSON.stringify(turn))
  }
})

// Blocker 2: a channel that reports isGroup false for a group it cannot name.
test("a group whose channel says isGroup false still gets nothing", async () => {
  const p = await plugin({ hostConfig: { channels: { googlechat: { allowFrom: ["4242"], streaming: "off" } } } })
  const { sent } = await p.turn({ key: "agent:main:googlechat:group:spaces-a", channel: "googlechat", conversation: "spaces-a", isGroup: false })
  assert.equal(sent[0], undefined)
  assert.deepEqual(promptCalls(p.calls()), ["hook-prompt --plain"])
})

// Blocker 1: every DM shares agent:main:main by default. The owner's turn ends
// without a reply, and the next message in the session is a stranger's.
test("a receipt left by an owner turn with no reply never reaches a stranger's DM", async () => {
  const p = await plugin()
  await p.turn({ kinds: [] })
  const { sent } = await p.turn({ sender: "7", prompt: "hi there, zorbit?" })
  assert.equal(sent[0], undefined)
})

test("a receipt left by an owner turn with no reply never reaches a heartbeat run's reply", async () => {
  const p = await plugin()
  await p.turn({ kinds: [] })
  const { sent } = await p.turn({ dispatch: false, trigger: "heartbeat", prompt: "heartbeat" })
  assert.equal(sent[0], undefined)
})

// A heartbeat poll runs on agent:main:main, and the stamp its prompt hook
// wrote hid the main DM from recall for twenty minutes (#5270).
test("a heartbeat run never calls deja's prompt hook", async () => {
  const p = await plugin()
  await p.turn({ dispatch: false, trigger: "heartbeat", prompt: "zorbit heartbeat" })
  let calls = ""
  try {
    calls = p.calls()
  } catch {}
  assert.deepEqual(promptCalls(calls), [])
  await p.turn({ prompt: "zorbit again" })
  assert.equal(promptCalls(p.calls()).length, 1)
})

test("a reply from another run in the same session does not take the receipt", async () => {
  const p = await plugin()
  await p.turn({ kinds: [] })
  // A reply arriving for the next run, before that run's own prompt hook.
  const out = await p.hooks.reply_payload_sending(
    { kind: "final", channel: "telegram", sessionKey: "agent:main:main", runId: "run-99", payload: { text: "late" } },
    { channelId: "telegram", conversationId: "telegram:4242", senderId: "4242", runId: "run-99" },
  )
  assert.equal(out, undefined)
})

// Nit 3: block-stream chunks are not the reply's end.
test("block-stream chunks pass untouched and the final reply carries the receipt", async () => {
  const p = await plugin()
  const { sent } = await p.turn({ kinds: ["block", "block", "final"] })
  assert.equal(sent[0], undefined)
  assert.equal(sent[1], undefined)
  assert.deepEqual(blocksOf(sent[2]), [{ type: "context", text: "_" + LINE + "_" }])
})

test("the session-start digest stays --plain on a channel, so its notes are not spent there", async () => {
  const p = await plugin()
  await p.turn()
  assert.doesNotMatch(p.calls(), /^hook-context$/m)
})

test("only a channel set to send replies whole counts as whole; anything else is streaming", () => {
  assert.equal(wholeReplies(OWNER, "telegram"), true)
  assert.equal(wholeReplies({ channels: { telegram: { streaming: "off" } } }, "telegram"), true)
  const streaming = [
    {},
    { channels: { telegram: {} } },
    { channels: { telegram: { streaming: { mode: "partial" } } } },
    { channels: { telegram: { streaming: { mode: "off", block: { enabled: true } } } } },
    { channels: { telegram: { streaming: "off", accounts: { work: { streaming: "partial" } } } } },
    { channels: { telegram: { streaming: "off" } }, agents: { defaults: { blockStreamingDefault: "on" } } },
  ]
  for (const cfg of streaming) assert.equal(wholeReplies(cfg, "telegram"), false, JSON.stringify(cfg))
})

// deja marks a receipt shown when it prints it. Where the reply streams there
// is no final reply to carry it, so it must not be asked for.
test("a DM on a streaming channel makes no JSON call", async () => {
  for (const telegram of [{ allowFrom: ["4242"] }, { allowFrom: ["4242"], streaming: { mode: "partial" } }]) {
    const p = await plugin({ hostConfig: { channels: { telegram } } })
    const { built, sent } = await p.turn({ kinds: ["block"] })
    assert.equal(built.prependContext, CONTEXT)
    assert.equal(sent[0], undefined)
    assert.deepEqual(promptCalls(p.calls()), ["hook-prompt --plain"], JSON.stringify(telegram))
  }
})

// session.scope "global" gives every chat one key with no group word in it, so
// the delivery's own chat type is the backstop.
test("a reply the delivery does not call direct gets nothing", async () => {
  for (const chatType of [null, "group"]) {
    const p = await plugin()
    const { sent } = await p.turn({ key: "global", chatType })
    assert.equal(sent[0], undefined, String(chatType))
  }
})

test("the older blockStreaming switch counts as streaming, on the channel or an account", () => {
  assert.equal(wholeReplies({ channels: { telegram: { streaming: "off", blockStreaming: true } } }, "telegram"), false)
  assert.equal(wholeReplies({ channels: { telegram: { streaming: "off", accounts: { work: { blockStreaming: true } } } } }, "telegram"), false)
  assert.equal(wholeReplies({ channels: { telegram: { streaming: "off", blockStreaming: false } } }, "telegram"), true)
})

test("a Slack DM's reply matches its conversation through OpenClaw's prefixes", async () => {
  assert.equal(conversationOf("slack:channel:D0123", "slack"), "d0123")
  assert.equal(conversationOf("channel:D0123", "slack"), "d0123")
  assert.equal(conversationOf("user:U9", "slack"), "u9")
  const p = await plugin({ hostConfig: { channels: { slack: { allowFrom: ["U9"], streaming: { mode: "off" } } } } })
  const { sent } = await p.turn({ key: "agent:main:slack:direct:u9", channel: "slack", sender: "U9", conversation: "D0123", replyConversation: "slack:channel:D0123" })
  assert.deepEqual(blocksOf(sent[0]), [{ type: "context", text: "_" + LINE + "_" }])
})

test("a second prompt hook in the same run keeps the receipt deja already spent", async () => {
  const p = await plugin()
  const { sent } = await p.turn({ prompts: 2 })
  assert.deepEqual(blocksOf(sent[0]), [{ type: "context", text: "_" + LINE + "_" }])
  assert.deepEqual(promptCalls(p.calls()), ["hook-prompt", "hook-prompt --plain"])
})
