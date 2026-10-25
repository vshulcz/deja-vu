import { test } from "node:test"
import assert from "node:assert/strict"

import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"

import { DejaPlugin } from "../index.js"
import { v1Client } from "../lib.js"

const LINE = 'deja-vu — from Codex CLI, 3 weeks ago: "drain the zorbit queue with --grace 40" (via zorbit)'

// A stand-in deja that logs every call with its arguments and answers
// hook-prompt with a recall and the person's line.
function stand() {
  const dir = mkdtempSync(join(tmpdir(), "deja-oc-notes-"))
  const bin = join(dir, "deja")
  const calls = join(dir, "calls")
  const reply = JSON.stringify({ systemMessage: LINE, hookSpecificOutput: { hookEventName: "UserPromptSubmit", additionalContext: "RECALL-ZORBIT" } })
  writeFileSync(
    bin,
    `#!/bin/sh\nif [ "$1" = version ]; then echo 0.0.0; exit 0; fi\nif [ "$1" = warmup-status ]; then exit 0; fi\nin=$(cat)\nprintf '%s\\t%s\\n' "$*" "$in" >> ${calls}\n` +
      `if [ "$1" = hook-prompt ]; then case "$in" in *zorbit*) printf '%s\\n' '${reply}' ;; esac; fi\n`,
    { mode: 0o755 },
  )
  const logged = () =>
    readFileSync(calls, "utf8")
      .split("\n")
      .filter(Boolean)
      .map((l) => {
        const [args, input] = l.split("\t")
        return { args, input: input ? JSON.parse(input) : undefined }
      })
  return { dir, bin, logged }
}

async function withConfigHome(dir, run) {
  const previous = process.env.XDG_CONFIG_HOME
  process.env.XDG_CONFIG_HOME = dir
  try {
    await run()
  } finally {
    if (previous === undefined) delete process.env.XDG_CONFIG_HOME
    else process.env.XDG_CONFIG_HOME = previous
    rmSync(dir, { recursive: true, force: true })
  }
}

const turn = (sid, text) => ({ messages: [{ info: { id: "m1", role: "user", sessionID: sid }, parts: [{ type: "text", text }] }] })

// The per-prompt line reached nobody in opencode: the plugin kept the model's
// half of the reply and dropped the person's, while deja counted it shown.
test("the per-prompt line goes to the person as a toast", async () => {
  const { dir, bin, logged } = stand()
  const toasts = []
  const client = { tui: { showToast: async ({ body }) => toasts.push(body.message) } }
  await withConfigHome(dir, async () => {
    const hooks = await DejaPlugin({ client, directory: dir }, { bin })
    const out = turn("ses_main", "how should the zorbit queue drain?")
    await hooks["experimental.chat.messages.transform"]({ sessionID: "ses_main" }, out)
    assert.deepEqual(toasts, [LINE])
    assert.ok(out.messages[0].parts[0].text.endsWith("RECALL-ZORBIT"), "the model lost its recall")
    assert.ok(!out.messages[0].parts[0].text.includes("deja-vu —"), "the person's line went to the model")
    assert.equal(logged().find((c) => c.args.startsWith("hook-prompt")).args, "hook-prompt")
  })
})

// A 2.x server plugin has no toast. deja is told so, and keeps its lines for a
// host that shows them instead of stamping them shown.
test("with no toast to show them in, deja is asked to keep its lines", async () => {
  const { dir, bin, logged } = stand()
  await withConfigHome(dir, async () => {
    const hooks = await DejaPlugin({ client: v1Client({}), directory: dir }, { bin })
    await hooks["experimental.chat.system.transform"]({ sessionID: "ses_main" }, { system: [] })
    const out = turn("ses_main", "how should the zorbit queue drain?")
    await hooks["experimental.chat.messages.transform"]({ sessionID: "ses_main" }, out)
    const args = logged().map((c) => c.args)
    assert.ok(args.includes("hook-context --strict"), args.join(" | "))
    assert.ok(args.includes("hook-prompt --strict"), args.join(" | "))
    assert.ok(out.messages[0].parts[0].text.endsWith("RECALL-ZORBIT"), "the model lost its recall")
  })
})

// A task sub-agent's prompt is the parent's instructions. The parent was given
// the recall; the sub-agent is asked about nothing, and only stamped live.
test("a sub-agent's turn is not searched again", async () => {
  const { dir, bin, logged } = stand()
  const toasts = []
  const client = {
    tui: { showToast: async ({ body }) => toasts.push(body.message) },
    session: { get: async ({ path }) => ({ data: { id: path.id, parentID: path.id === "ses_child" ? "ses_parent" : undefined } }) },
  }
  await withConfigHome(dir, async () => {
    const hooks = await DejaPlugin({ client, directory: dir }, { bin })
    const out = turn("ses_child", "find how the zorbit queue drains on deploy")
    await hooks["experimental.chat.messages.transform"]({ sessionID: "ses_child" }, out)
    const call = logged().find((c) => c.args.startsWith("hook-prompt"))
    assert.ok(call, "the sub-agent was not stamped live")
    assert.deepEqual(call.input, { prompt: "", session_id: "ses_child", parent_session_id: "ses_parent", cwd: dir })
    assert.equal(out.messages[0].parts[0].text, "find how the zorbit queue drains on deploy")
    assert.deepEqual(toasts, [])
  })
})
