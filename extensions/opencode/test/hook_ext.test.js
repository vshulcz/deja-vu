import { test } from "node:test"
import assert from "node:assert/strict"
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"

import { DejaPlugin } from "../index.js"
import { parseHookExtEnvelope, TOOL_SPECS } from "../lib.js"

function withEnv(dir, fn) {
  const prevCfg = process.env.XDG_CONFIG_HOME
  const prevCache = process.env.XDG_CACHE_HOME
  process.env.XDG_CONFIG_HOME = dir
  process.env.XDG_CACHE_HOME = join(dir, ".cache")
  return Promise.resolve()
    .then(fn)
    .finally(() => {
      if (prevCfg === undefined) delete process.env.XDG_CONFIG_HOME
      else process.env.XDG_CONFIG_HOME = prevCfg
      if (prevCache === undefined) delete process.env.XDG_CACHE_HOME
      else process.env.XDG_CACHE_HOME = prevCache
    })
}

function makeExtStub(mode) {
  const dir = mkdtempSync(join(tmpdir(), "deja-oc-ext-"))
  const bin = join(dir, "deja")
  const calls = join(dir, "calls.log")
  writeFileSync(
    bin,
    `#!/usr/bin/env node
const fs = require("node:fs");
const args = process.argv.slice(2);
if (args[0] === "version") { process.stdout.write("1.0.0\\n"); process.exit(0); }
const needsStdin = args[0] === "hook-ext" || (args[0] && args[0].startsWith("hook-"));
const input = needsStdin ? fs.readFileSync(0, "utf8") : "";
fs.appendFileSync(${JSON.stringify(calls)}, JSON.stringify({ args, input }) + "\\n");
if (args[0] === "hook-ext") {
  const op = args[args.indexOf("--op") + 1] || "";
  const req = input ? JSON.parse(input) : {};
  if (${JSON.stringify(mode)} === "deny") {
    process.stdout.write(JSON.stringify({ ok: false, kind: "error", error: "blocked by policy" }) + "\\n");
    process.exit(1);
  }
  if (${JSON.stringify(mode)} === "legacy") {
    process.stderr.write("unknown command hook-ext\\n");
    process.exit(1);
  }
  if (op === "context") {
    process.stdout.write(JSON.stringify({ ok: true, kind: "hook", context: "EXT-CTX", note: "EXT-NOTE" }) + "\\n");
  } else if (op === "prompt") {
    process.stdout.write(JSON.stringify({ ok: true, kind: "hook", context: "EXT-PROMPT", note: "EXT-TOAST" }) + "\\n");
  } else if (op === "tool-before") {
    if (req.tool === "Task") process.stdout.write(JSON.stringify({ ok: true, kind: "hook", updatedInput: { prompt: "EXT-SPAWN" } }) + "\\n");
    else process.stdout.write(JSON.stringify({ ok: true, kind: "hook", context: "EXT-READ-NOTE" }) + "\\n");
  } else if (op === "tool-after") {
    process.stdout.write(JSON.stringify({ ok: true, kind: "hook", context: "EXT-FIX" }) + "\\n");
  } else if (op === "tool") {
    process.stdout.write(JSON.stringify({ ok: true, kind: "success", output: "EXT-TOOL:" + req.tool + ":" + (req.mode || "") + ":" + (req.limit ?? "") + ":" + (req.offset ?? "") }) + "\\n");
  } else {
    process.stdout.write(JSON.stringify({ ok: true }) + "\\n");
  }
  process.exit(0);
}
if (args[0] === "hook-context") process.stdout.write(JSON.stringify({ hookSpecificOutput: { additionalContext: "LEGACY-CTX" } }) + "\\n");
if (args[0] === "recall") process.stdout.write("LEGACY-RECALL\\n");
`,
    { mode: 0o755 },
  )
  const logged = () => {
    try {
      return readFileSync(calls, "utf8")
        .split("\n")
        .filter(Boolean)
        .map((l) => JSON.parse(l))
    } catch {
      return []
    }
  }
  return { dir, bin, logged, cleanup: () => rmSync(dir, { recursive: true, force: true }) }
}

test("parseHookExtEnvelope only accepts JSON objects with boolean ok", () => {
  assert.equal(parseHookExtEnvelope(""), null)
  assert.equal(parseHookExtEnvelope("plain output"), null)
  assert.equal(parseHookExtEnvelope('{"ok":"yes"}'), null)
  assert.deepEqual(parseHookExtEnvelope('{"ok":false,"error":"nope"}\n'), { ok: false, error: "nope" })
})

test("routes hooks and tools through hook-ext without calling legacy subcommands", async () => {
  const s = makeExtStub("ok")
  const toasts = []
  try {
    await withEnv(s.dir, async () => {
      const hooks = await DejaPlugin({ client: { tui: { showToast: async ({ body }) => toasts.push(body.message) } }, directory: s.dir }, { bin: s.bin })
      const sys = { system: ["Base"] }
      await hooks["experimental.chat.system.transform"]({ sessionID: "ses_1" }, sys)
      assert.equal(sys.system[0], "Base\n\nEXT-CTX")

      const msg = { messages: [{ info: { id: "u1", role: "user", sessionID: "ses_1" }, parts: [{ type: "text", text: "hi" }] }] }
      await hooks["experimental.chat.messages.transform"]({ sessionID: "ses_1" }, msg)
      assert.equal(msg.messages[0].parts[0].text, "hi\n\nEXT-PROMPT")
      assert.deepEqual(toasts, ["EXT-NOTE", "EXT-TOAST"])

      const bef = { args: { prompt: "raw" } }
      await hooks["tool.execute.before"]({ tool: "task", sessionID: "ses_1" }, bef)
      assert.equal(bef.args.prompt, "EXT-SPAWN")

      const fileOut = { output: "file body" }
      await hooks["tool.execute.after"]({ tool: "read", sessionID: "ses_1", args: { filePath: "a.go" } }, fileOut)
      assert.equal(fileOut.output, "file body\n\nEXT-READ-NOTE")

      const bashOut = { output: "failed" }
      await hooks["tool.execute.after"]({ tool: "bash", sessionID: "ses_1", args: { command: "make" } }, bashOut)
      assert.equal(bashOut.output, "failed\n\nEXT-FIX")

      await hooks["experimental.session.compacting"]({ sessionID: "ses_1" })
      await hooks.event({ event: { type: "session.idle", properties: { sessionID: "ses_1" } } })

      const recallOut = await hooks[TOOL_SPECS].deja_recall.execute({ query: "q", limit: 3.9, offset: 6.2 })
      assert.equal(recallOut, "EXT-TOOL:deja_recall::3:6")
      const blameOut = await hooks[TOOL_SPECS].deja_blame.execute({ path: "a.go" })
      assert.equal(blameOut, "EXT-TOOL:deja_blame:json::")

      const cmds = s.logged().map((c) => c.args[0])
      assert.ok(cmds.every((c) => c === "hook-ext"), cmds.join(","))
    })
  } finally {
    s.cleanup()
  }
})

test("ok:false on non-zero exit blocks legacy fallback", async () => {
  const s = makeExtStub("deny")
  try {
    await withEnv(s.dir, async () => {
      const hooks = await DejaPlugin({ client: {}, directory: s.dir }, { bin: s.bin })
      const out = await hooks[TOOL_SPECS].deja_recall.execute({ query: "secret" })
      assert.equal(out, "blocked by policy")
      const sys = { system: ["Base"] }
      await hooks["experimental.chat.system.transform"]({ sessionID: "ses_d" }, sys)
      assert.equal(sys.system[0], "Base")
      const cmds = s.logged().map((c) => c.args[0])
      assert.ok(!cmds.includes("recall") && !cmds.includes("hook-context"), cmds.join(","))
    })
  } finally {
    s.cleanup()
  }
})

test("legacy binary without hook-ext falls back cleanly", async () => {
  const s = makeExtStub("legacy")
  try {
    await withEnv(s.dir, async () => {
      const hooks = await DejaPlugin({ client: {}, directory: s.dir }, { bin: s.bin })
      const out = await hooks[TOOL_SPECS].deja_recall.execute({ query: "term" })
      assert.equal(out, "LEGACY-RECALL")
      const sys = { system: ["Base"] }
      await hooks["experimental.chat.system.transform"]({ sessionID: "ses_l" }, sys)
      assert.equal(sys.system[0], "Base\n\nLEGACY-CTX")
      const extCalls = s.logged().filter((c) => c.args[0] === "hook-ext")
      assert.equal(extCalls.length, 1, "expected capability cache after first legacy probe")
    })
  } finally {
    s.cleanup()
  }
})
