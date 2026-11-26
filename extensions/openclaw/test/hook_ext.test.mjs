import { test } from "node:test"
import assert from "node:assert/strict"
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"

function makeStub(mode) {
  const dir = mkdtempSync(join(tmpdir(), "deja-openclaw-ext-"))
  const work = join(dir, "work")
  mkdirSync(work)
  const log = join(dir, "calls.log")
  const stub = join(dir, "deja")
  const script = `#!/usr/bin/env node
const fs = require("node:fs");
const args = process.argv.slice(2);
const needsStdin = args[0] === "hook-ext" || (args[0]?.startsWith("hook-") && !args.includes("--notes"));
const raw = needsStdin ? fs.readFileSync(0, "utf8") : "";
fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify({ args, raw, cwd: fs.realpathSync(process.cwd()) }) + "\\n");

if (args[0] === "--version" || args[0] === "version") {
  process.stdout.write("deja 0.25.1\\n");
  process.exit(0);
}

const mode = ${JSON.stringify(mode)};
if (args[0] === "hook-ext") {
  if (mode === "legacy") {
    process.stderr.write("unknown command: hook-ext\\n");
    process.exit(2);
  }
  const op = args[args.indexOf("--op") + 1];
  const req = JSON.parse(raw || "{}");
  if (mode === "deny") {
    process.stdout.write(JSON.stringify({ ok: false, kind: "error", error: "blocked by policy: " + op }) + "\\n");
    process.exit(1);
  }
  if (op === "context" && req.notes_only) {
    process.stdout.write(JSON.stringify({ ok: true, kind: "notes", note: "note line", output: "note line" }) + "\\n");
    process.exit(0);
  }
  if (op === "context") {
    process.stdout.write(JSON.stringify({ ok: true, kind: "hook", context: "<digest>" + (req.cwd || "") }) + "\\n");
    process.exit(0);
  }
  if (op === "prompt") {
    process.stdout.write(JSON.stringify({
      ok: true,
      kind: "hook",
      context: "<deja-recall>ext prompt</deja-recall>",
      note: req.plain ? "" : 'deja-vu — from Codex CLI, today: "ext topic"',
      receipt: req.plain ? "" : '↺ from Codex CLI, today: "ext topic" · deja',
    }) + "\\n");
    process.exit(0);
  }
  if (op === "tool-before" || op === "tool-after") {
    process.stdout.write(JSON.stringify({ ok: true, kind: "hook", context: op + ":" + (req.tool || "") }) + "\\n");
    process.exit(0);
  }
  if (op === "precompact" || op === "session-end") {
    process.stdout.write(JSON.stringify({ ok: true }) + "\\n");
    process.exit(0);
  }
  if (op === "tool") {
    process.stdout.write(JSON.stringify({ ok: true, kind: "success", output: "ext-tool:" + req.tool + ":" + (req.limit || 0) + ":" + (req.offset || 0) }) + "\\n");
    process.exit(0);
  }
  if (op === "slash") {
    process.stdout.write(JSON.stringify({ ok: true, kind: "success", output: "ext-slash:" + (req.args || "") }) + "\\n");
    process.exit(0);
  }
}

if (args[0] === "hook-context" && args.includes("--notes")) {
  process.stdout.write("legacy note");
  process.exit(0);
}
if (args[0] === "hook-context") {
  process.stdout.write("<legacy-digest>");
  process.exit(0);
}
if (args[0] === "hook-prompt") {
  process.stdout.write("<legacy-prompt>");
  process.exit(0);
}
if (args[0] === "hook-tool") {
  process.stdout.write("legacy-tool-before");
  process.exit(0);
}
if (args[0] === "hook-tool-after") {
  process.stdout.write("legacy-tool-after");
  process.exit(0);
}
if (args[0] === "recall" || args[0] === "--framed" || args[0] === "search") {
  process.stdout.write("legacy:" + args.join(" "));
  process.exit(0);
}
`
  writeFileSync(stub, script)
  chmodSync(stub, 0o755)
  return { dir, work, stub, log }
}

async function setupPlugin(mode) {
  const { dir, work, stub, log } = makeStub(mode)
  const hooks = new Map()
  const registeredHooks = new Map()
  const middlewares = []
  const tools = new Map()
  let slashCmd = null

  const prevState = process.env.OPENCLAW_STATE_DIR
  process.env.OPENCLAW_STATE_DIR = dir
  try {
    const { default: plugin } = await import("../index.mjs?" + Math.random())
    plugin.register({
      id: "deja-vu",
      config: {
        hooks: { internal: { enabled: true } },
        plugins: { entries: { "deja-vu": { hooks: { allowConversationAccess: true } } } },
        channels: { telegram: { allowFrom: ["4242"], streaming: { mode: "off" } } },
      },
      pluginConfig: { bin: stub },
      on: (name, fn) => hooks.set(name, fn),
      registerHook: (name, fn) => registeredHooks.set(name, fn),
      registerTool: (factory, opts) => tools.set(opts.name, factory),
      registerAgentToolResultMiddleware: (fn) => middlewares.push(fn),
      registerCommand: (cmd) => {
        slashCmd = cmd
      },
      logger: { warn() {} },
    })
  } finally {
    if (prevState === undefined) delete process.env.OPENCLAW_STATE_DIR
    else process.env.OPENCLAW_STATE_DIR = prevState
  }

  const readCalls = () => {
    try {
      return readFileSync(log, "utf8")
        .trim()
        .split("\n")
        .filter(Boolean)
        .map((line) => JSON.parse(line))
    } catch {
      return []
    }
  }
  return { dir, work, hooks, registeredHooks, middlewares, tools, slashCmd, readCalls }
}

test(
  "routes OpenClaw hooks, tools, and /deja through hook-ext --host openclaw",
  { skip: process.platform === "win32" && "executable node shebang" },
  async () => {
    const p = await setupPlugin("ext")
    const bootstrapFiles = []
    await p.registeredHooks.get("agent:bootstrap")({
      context: { bootstrapFiles, sessionId: "s-1", workspaceDir: p.work },
    })
    assert.equal(bootstrapFiles.length, 1)
    assert.equal(bootstrapFiles[0].content, "<digest>" + p.work)

    await p.hooks.get("before_dispatch")(
      { sessionKey: "agent:main:main", channel: "telegram", senderId: "4242", isGroup: false },
      { sessionKey: "agent:main:main", channelId: "telegram", conversationId: "4242", senderId: "4242" },
    )
    const promptRes = await p.hooks.get("before_prompt_build")(
      { prompt: "how did we deploy?" },
      { sessionKey: "agent:main:main", channel: "telegram", conversationId: "4242", senderId: "4242", runId: "run-1" },
    )
    assert.equal(promptRes.prependContext, "<deja-recall>ext prompt</deja-recall>")

    const reply = await p.hooks.get("reply_payload_sending")(
      { kind: "final", channel: "telegram", sessionKey: "agent:main:main", runId: "run-1", usageState: { chatType: "direct" }, payload: { text: "ok" } },
      { channelId: "telegram", conversationId: "4242", senderId: "4242", runId: "run-1" },
    )
    assert.equal(reply.payload.presentation.blocks[0].text, '_↺ from Codex CLI, today: "ext topic" · deja_')

    const readOut = await p.middlewares[0]({ toolName: "read", args: { path: "foo.go" }, cwd: p.work, result: { content: [] } }, { sessionId: "s-1" })
    assert.equal(readOut.result.content[0].text, "tool-before:read")
    const bashOut = await p.middlewares[0]({ toolName: "exec", args: { command: "go test" }, cwd: p.work, result: { content: [{ type: "text", text: "FAIL" }] } }, { sessionId: "s-1" })
    assert.equal(bashOut.result.content[1].text, "tool-after:bash")

    await p.hooks.get("before_compaction")({ sessionFile: "/tmp/t.jsonl" }, { sessionKey: "agent:main:main" })
    await p.hooks.get("session_end")({ sessionId: "s-1" }, { sessionKey: "agent:main:main", workspaceDir: p.work })

    const rec = await p.tools.get("deja_recall")({ workspaceDir: p.work }).execute("1", { query: "zorbit", limit: 3, offset: 2 })
    assert.equal(rec.content[0].text, "ext-tool:deja_recall:3:2")
    const fix = await p.tools.get("deja_fix")({ workspaceDir: p.work }).execute("2", { error: "boom" })
    assert.equal(fix.content[0].text, "ext-tool:deja_fix:0:0")
    const blame = await p.tools.get("deja_blame")({ workspaceDir: p.work }).execute("3", { path: "a.go" })
    assert.equal(blame.content[0].text, "ext-tool:deja_blame:0:0")

    const slashRes = await p.slashCmd.handler({ args: "zorbit" })
    assert.equal(slashRes.text, "ext-slash:zorbit\n\nnote line")

    const calls = p.readCalls().filter((c) => c.args[0] !== "--version" && c.args[0] !== "version")
    assert.ok(calls.length > 0)
    for (const c of calls) {
      assert.equal(c.args[0], "hook-ext", `expected hook-ext, got ${c.args.join(" ")}`)
      assert.equal(c.args[2], "openclaw")
    }
    const ctxCall = calls.find((c) => c.args[4] === "context" && !JSON.parse(c.raw).notes_only)
    assert.equal(ctxCall.cwd, realpathSync(p.work))
  },
)

test(
  "does not fall back to legacy subcommands when hook-ext returns ok:false",
  { skip: process.platform === "win32" && "executable node shebang" },
  async () => {
    const p = await setupPlugin("deny")
    const rec = await p.tools.get("deja_recall")({ workspaceDir: p.work }).execute("1", { query: "zorbit" })
    assert.equal(rec.content[0].text, "blocked by policy: tool")
    const slashRes = await p.slashCmd.handler({ args: "zorbit" })
    assert.equal(slashRes.text, "blocked by policy: slash")
    const promptRes = await p.hooks.get("before_prompt_build")(
      { prompt: "how did we deploy?" },
      { sessionKey: "agent:main:main" },
    )
    assert.equal(promptRes, undefined)

    const nonVersion = p.readCalls().filter((c) => c.args[0] !== "--version" && c.args[0] !== "version")
    assert.ok(nonVersion.every((c) => c.args[0] === "hook-ext"), "legacy command should never run after ok:false")
  },
)

test(
  "falls back to legacy subcommands when hook-ext is unsupported",
  { skip: process.platform === "win32" && "executable node shebang" },
  async () => {
    const p = await setupPlugin("legacy")
    const bootstrapFiles = []
    await p.registeredHooks.get("agent:bootstrap")({
      context: { bootstrapFiles, sessionId: "s-1", workspaceDir: p.work },
    })
    assert.equal(bootstrapFiles[0].content, "<legacy-digest>")

    const promptRes = await p.hooks.get("before_prompt_build")(
      { prompt: "how did we deploy?" },
      { sessionKey: "agent:main:main" },
    )
    assert.equal(promptRes.prependContext, "<legacy-prompt>")

    const bashOut = await p.middlewares[0]({ toolName: "exec", args: { command: "go test" }, cwd: p.work, result: { content: [{ type: "text", text: "FAIL" }] } }, { sessionId: "s-1" })
    assert.equal(bashOut.result.content[1].text, "legacy-tool-after")

    const rec = await p.tools.get("deja_recall")({ workspaceDir: p.work }).execute("1", { query: "zorbit" })
    assert.equal(rec.content[0].text, "legacy:recall --limit 5 zorbit")
    const slashRes = await p.slashCmd.handler({ args: "zorbit" })
    assert.equal(slashRes.text, "legacy:search zorbit\n\nlegacy note")
  },
)
