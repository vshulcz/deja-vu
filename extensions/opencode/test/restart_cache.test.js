import { test } from "node:test"
import assert from "node:assert/strict"

import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"

import { DejaPlugin } from "../index.js"
import entry from "../server.js"

function stand() {
  const dir = mkdtempSync(join(tmpdir(), "deja-oc-restart-"))
  const bin = join(dir, "deja")
  const ctxCount = join(dir, "ctx_count")
  const promptCount = join(dir, "prompt_count")
  const calls = join(dir, "calls")
  writeFileSync(
    bin,
    `#!/usr/bin/env node
const fs = require("node:fs");
const args = process.argv.slice(2);
const cmd = args[0] || "";
if (cmd === "version") { process.stdout.write("0.0.0\\n"); process.exit(0); }
if (cmd === "warmup-status") { process.exit(0); }
const input = fs.readFileSync(0, "utf8");
fs.appendFileSync(${JSON.stringify(calls)}, cmd + "\\t" + input + "\\n");
if (cmd === "hook-context") {
  let n = 0;
  try { n = Number(fs.readFileSync(${JSON.stringify(ctxCount)}, "utf8")) || 0; } catch {}
  n++;
  fs.writeFileSync(${JSON.stringify(ctxCount)}, String(n));
  process.stdout.write(JSON.stringify({ hookSpecificOutput: { additionalContext: "DIGEST-" + n } }) + "\\n");
} else if (cmd === "hook-prompt") {
  let payload = {};
  try { payload = JSON.parse(input); } catch {}
  if (!payload.prompt) process.exit(0);
  let n = 0;
  try { n = Number(fs.readFileSync(${JSON.stringify(promptCount)}, "utf8")) || 0; } catch {}
  n++;
  fs.writeFileSync(${JSON.stringify(promptCount)}, String(n));
  process.stdout.write(JSON.stringify({ hookSpecificOutput: { additionalContext: "<deja-recall>\\nPROMPT-" + n + "\\n</deja-recall>" } }) + "\\n");
}
`,
    { mode: 0o755 },
  )
  const count = (file) => {
    try {
      return Number(readFileSync(file, "utf8")) || 0
    } catch {
      return 0
    }
  }
  return {
    dir,
    bin,
    ctxCalls: () => count(ctxCount),
    promptCalls: () => count(promptCount),
    cleanup: () => rmSync(dir, { recursive: true, force: true }),
  }
}

async function withEnv(dir, fn) {
  const prevConfig = process.env.XDG_CONFIG_HOME
  const prevCache = process.env.XDG_CACHE_HOME
  process.env.XDG_CONFIG_HOME = dir
  process.env.XDG_CACHE_HOME = join(dir, ".cache")
  try {
    return await fn()
  } finally {
    if (prevConfig === undefined) delete process.env.XDG_CONFIG_HOME
    else process.env.XDG_CONFIG_HOME = prevConfig
    if (prevCache === undefined) delete process.env.XDG_CACHE_HOME
    else process.env.XDG_CACHE_HOME = prevCache
  }
}

test("1.x DejaPlugin persists digest and per-prompt recall across plugin restarts", async () => {
  const s = stand()
  try {
    await withEnv(s.dir, async () => {
      const p1 = await DejaPlugin({ client: {}, directory: s.dir }, { bin: s.bin })
      const sys1 = { system: ["base"] }
      await p1["experimental.chat.system.transform"]({ sessionID: "ses_1" }, sys1)
      assert.equal(sys1.system[0], "base\n\nDIGEST-1")

      const m1 = { info: { id: "m1", role: "user", sessionID: "ses_1" }, parts: [{ type: "text", text: "first question" }] }
      await p1["experimental.chat.messages.transform"]({ sessionID: "ses_1" }, { messages: [m1] })
      assert.equal(m1.parts[0].text, "first question\n\n<deja-recall>\nPROMPT-1\n</deja-recall>")

      // Simulate OpenCode restart: new plugin instance, fresh DB-rebuilt clean messages.
      const p2 = await DejaPlugin({ client: {}, directory: s.dir }, { bin: s.bin })
      const sys2 = { system: ["base"] }
      await p2["experimental.chat.system.transform"]({ sessionID: "ses_1" }, sys2)
      assert.equal(sys2.system[0], "base\n\nDIGEST-1")
      assert.equal(s.ctxCalls(), 1)

      const m1Rebuilt = { info: { id: "m1", role: "user", sessionID: "ses_1" }, parts: [{ type: "text", text: "first question" }] }
      const a1 = { info: { id: "a1", role: "assistant", sessionID: "ses_1" }, parts: [{ type: "text", text: "done" }] }
      const m2 = { info: { id: "m2", role: "user", sessionID: "ses_1" }, parts: [{ type: "text", text: "second question" }] }
      await p2["experimental.chat.messages.transform"]({ sessionID: "ses_1" }, { messages: [m1Rebuilt, a1, m2] })
      assert.equal(m1Rebuilt.parts[0].text, "first question\n\n<deja-recall>\nPROMPT-1\n</deja-recall>")
      assert.equal(m2.parts[0].text, "second question\n\n<deja-recall>\nPROMPT-2\n</deja-recall>")
      assert.equal(s.promptCalls(), 2)
    })
  } finally {
    s.cleanup()
  }
})

test("2.x server.js persists digest and per-prompt recall across server setup restarts", async () => {
  const s = stand()
  try {
    await withEnv(s.dir, async () => {
      const makeCtx = () => {
        const contextHooks = []
        return {
          contextHooks,
          ctx: {
            location: { directory: s.dir },
            options: { bin: s.bin },
            session: { hook: async (ev, fn) => { if (ev === "context") contextHooks.push(fn) } },
            tool: { hook: async () => {}, transform: async () => {} },
          },
        }
      }

      const h1 = makeCtx()
      await entry.setup(h1.ctx)
      const req1 = {
        sessionID: "ses_2",
        system: [{ type: "text", text: "base" }],
        messages: [{ id: "m1", role: "user", content: [{ type: "text", text: "first question" }] }],
      }
      await h1.contextHooks[0](req1)
      assert.equal(req1.system[0].text, "base\n\nDIGEST-1")
      assert.equal(req1.messages[0].content[0].text, "first question\n\n<deja-recall>\nPROMPT-1\n</deja-recall>")

      const h2 = makeCtx()
      await entry.setup(h2.ctx)
      const req2 = {
        sessionID: "ses_2",
        system: [{ type: "text", text: "base" }],
        messages: [
          { id: "m1", role: "user", content: [{ type: "text", text: "first question" }] },
          { id: "a1", role: "assistant", content: [{ type: "text", text: "ok" }] },
          { id: "m2", role: "user", content: [{ type: "text", text: "second question" }] },
        ],
      }
      await h2.contextHooks[0](req2)
      assert.equal(req2.system[0].text, "base\n\nDIGEST-1")
      assert.equal(req2.messages[0].content[0].text, "first question\n\n<deja-recall>\nPROMPT-1\n</deja-recall>")
      assert.equal(req2.messages[2].content[0].text, "second question\n\n<deja-recall>\nPROMPT-2\n</deja-recall>")
      assert.equal(s.ctxCalls(), 1)
      assert.equal(s.promptCalls(), 2)
    })
  } finally {
    s.cleanup()
  }
})

test("compacting one session leaves other concurrent sessions intact and blocks stale epoch overwrite", async () => {
  const s = stand()
  try {
    await withEnv(s.dir, async () => {
      const p1 = await DejaPlugin({ client: {}, directory: s.dir }, { bin: s.bin })
      const p2Stale = await DejaPlugin({ client: {}, directory: s.dir }, { bin: s.bin })

      const mA1 = { info: { id: "mA1", role: "user", sessionID: "ses_A" }, parts: [{ type: "text", text: "topic alpha" }] }
      await p1["experimental.chat.messages.transform"]({ sessionID: "ses_A" }, { messages: [mA1] })
      const mB1 = { info: { id: "mB1", role: "user", sessionID: "ses_B" }, parts: [{ type: "text", text: "topic beta" }] }
      await p1["experimental.chat.messages.transform"]({ sessionID: "ses_B" }, { messages: [mB1] })
      assert.equal(mA1.parts[0].text, "topic alpha\n\n<deja-recall>\nPROMPT-1\n</deja-recall>")
      assert.equal(mB1.parts[0].text, "topic beta\n\n<deja-recall>\nPROMPT-2\n</deja-recall>")

      // Pre-load ses_A in stale instance at epoch 0 before p1 compacts ses_A.
      const mA1StaleProbe = { info: { id: "mA1", role: "user", sessionID: "ses_A" }, parts: [{ type: "text", text: "topic alpha" }] }
      await p2Stale["experimental.chat.messages.transform"]({ sessionID: "ses_A" }, { messages: [mA1StaleProbe] })
      assert.equal(mA1StaleProbe.parts[0].text, "topic alpha\n\n<deja-recall>\nPROMPT-1\n</deja-recall>")

      // Compact ses_A on p1.
      await p1["experimental.session.compacting"]({ sessionID: "ses_A" })

      // Flush p2Stale by adding a turn to ses_C so p2Stale writes disk: higher epoch on ses_A must win!
      const mC1 = { info: { id: "mC1", role: "user", sessionID: "ses_C" }, parts: [{ type: "text", text: "topic gamma" }] }
      await p2Stale["experimental.chat.messages.transform"]({ sessionID: "ses_C" }, { messages: [mC1] })

      // ses_B must still have PROMPT-2 re-attached cleanly without re-calling hook-prompt for mB1.
      const mB1Clean = { info: { id: "mB1", role: "user", sessionID: "ses_B" }, parts: [{ type: "text", text: "topic beta" }] }
      const mB2 = { info: { id: "mB2", role: "user", sessionID: "ses_B" }, parts: [{ type: "text", text: "beta next" }] }
      await p1["experimental.chat.messages.transform"]({ sessionID: "ses_B" }, { messages: [mB1Clean, mB2] })
      assert.equal(mB1Clean.parts[0].text, "topic beta\n\n<deja-recall>\nPROMPT-2\n</deja-recall>")
      assert.equal(mB2.parts[0].text, "beta next\n\n<deja-recall>\nPROMPT-4\n</deja-recall>")

      // A restarted instance processing post-compaction ses_A must fetch fresh recall instead of resurrected PROMPT-1.
      const p3 = await DejaPlugin({ client: {}, directory: s.dir }, { bin: s.bin })
      const mAAfter = { info: { id: "mA1", role: "user", sessionID: "ses_A" }, parts: [{ type: "text", text: "topic alpha" }] }
      await p3["experimental.chat.messages.transform"]({ sessionID: "ses_A" }, { messages: [mAAfter] })
      assert.equal(mAAfter.parts[0].text, "topic alpha\n\n<deja-recall>\nPROMPT-5\n</deja-recall>")
    })
  } finally {
    s.cleanup()
  }
})
