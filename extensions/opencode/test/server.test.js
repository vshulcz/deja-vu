import { test } from "node:test"
import assert from "node:assert/strict"

import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"

import * as main from "../index.js"
import * as entry from "../server.js"
import { jsonSchema, mcpWired, resultText, v1ToolName } from "../lib.js"

const plugin = entry.default

// opencode 2.x refuses a module without a default `{ id, setup | effect }`
// (#4151), and rejects a function there: the check wants an object.
test("the server entry is the definition 2.x loads", () => {
  assert.deepEqual(Object.keys(entry), ["default"])
  assert.equal(typeof plugin, "object")
  assert.equal(typeof plugin.id, "string")
  assert.equal(typeof plugin.setup, "function")
})

// 1.x from 1.3.4 resolves the same subpath and calls `server`; older 1.x loads
// index.js and calls every export, so that file keeps its one named export.
test("1.x gets the same plugin from either entry", () => {
  assert.equal(plugin.server, main.DejaPlugin)
  assert.deepEqual(Object.keys(main), ["DejaPlugin"])
  const pkg = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8"))
  assert.equal(pkg.exports["./server"], "./server.js")
  assert.equal(pkg.exports["."], "./index.js")
  assert.ok(pkg.files.includes("server.js"))
})

test("2.x keeps the MCP server under mcp.servers", () => {
  assert.equal(mcpWired(`{"mcp": {"servers": {"deja": {"command": ["deja", "mcp"]}}}}`), true)
  assert.equal(mcpWired(`{"mcp": {"servers": {"deja": {"command": ["deja", "mcp"], "enabled": false}}}}`), false)
  assert.equal(mcpWired(`{"mcp": {"servers": {"other": {}}}}`), false)
})

test("the helpers read 2.x's shapes", () => {
  assert.deepEqual(
    jsonSchema({ query: { type: "string", description: "q" }, limit: { type: "number", optional: true, description: "n" } }),
    {
      type: "object",
      properties: { query: { type: "string", description: "q" }, limit: { type: "number", description: "n" } },
      required: ["query"],
    },
  )
  assert.equal(resultText({ content: "plain" }), "plain")
  assert.equal(resultText({ content: [{ type: "text", text: "a" }, { type: "file" }, { type: "text", text: "b" }] }), "a\nb")
  assert.equal(resultText({ output: "out" }), "out")
  assert.equal(resultText(undefined), "")
  assert.equal(v1ToolName("subagent"), "task")
  assert.equal(v1ToolName("shell"), "bash")
  assert.equal(v1ToolName("read"), "read")
})

// A deja that answers every hook, so each seam can be seen to carry its line.
function stubDeja(dir) {
  const bin = join(dir, "deja")
  writeFileSync(
    bin,
    `#!/bin/sh
case "$1" in
  hook-context) echo '{"hookSpecificOutput":{"additionalContext":"past work"}}' ;;
  hook-prompt) echo '{"hookSpecificOutput":{"additionalContext":"recalled"}}' ;;
  hook-tool) cat >/dev/null; echo '{"hookSpecificOutput":{"updatedInput":{"prompt":"child brief + recall"},"additionalContext":"file history"}}' ;;
  hook-tool-after) echo '{"hookSpecificOutput":{"additionalContext":"ran next: go mod tidy"}}' ;;
  search) echo 'found it' ;;
  *) echo 0.0.0 ;;
esac
`,
    { mode: 0o755 },
  )
  return bin
}

// What setup registers, recorded the way 2.x's domains take it.
function fakeContext(directory, options) {
  const hooks = { session: {}, tool: {} }
  const tools = []
  const domain = (name) => ({
    hook: async (event, fn) => {
      ;(hooks[name][event] ||= []).push(fn)
    },
  })
  return {
    hooks,
    tools,
    ctx: {
      location: { directory },
      options,
      session: domain("session"),
      tool: {
        ...domain("tool"),
        transform: async (fn) => fn({ add: (t) => tools.push(t) }),
      },
    },
  }
}

async function withHome(run) {
  const dir = mkdtempSync(join(tmpdir(), "deja-oc2-"))
  const previous = process.env.XDG_CONFIG_HOME
  process.env.XDG_CONFIG_HOME = dir
  try {
    await run(dir)
  } finally {
    if (previous === undefined) delete process.env.XDG_CONFIG_HOME
    else process.env.XDG_CONFIG_HOME = previous
    rmSync(dir, { recursive: true, force: true })
  }
}

test("setup wires every channel the 1.x hooks have onto 2.x's seams", async () => {
  await withHome(async (dir) => {
    const { ctx, hooks, tools } = fakeContext(dir, { bin: stubDeja(dir) })
    await plugin.setup(ctx)

    assert.deepEqual(
      tools.map((t) => t.name),
      ["deja_recall", "deja_session", "deja_blame", "deja_fix", "deja_how", "deja_remember"],
    )
    const recall = tools[0]
    assert.deepEqual(recall.input.required, ["query"])
    assert.deepEqual(await recall.execute({ query: "flake" }), { content: "found it" })

    assert.equal(hooks.session.context.length, 1)
    assert.equal(hooks.session.compaction.length, 1)
    assert.equal(hooks.tool["execute.before"].length, 1)
    assert.equal(hooks.tool["execute.after"].length, 1)

    // The digest joins the first system part, and the prompt's own text part
    // carries the per-prompt recall.
    const context = {
      sessionID: "s1",
      system: [{ type: "text", text: "agent prompt" }],
      messages: [
        { role: "user", content: [{ type: "text", text: "why does TestRetry flake" }] },
        { role: "assistant", content: [{ type: "text", text: "looking" }] },
      ],
    }
    await hooks.session.context[0](context)
    assert.equal(context.system.length, 1)
    assert.equal(context.system[0].text, "past work\n\nagent prompt")
    assert.equal(context.messages[0].content[0].text, "why does TestRetry flake\n\nrecalled")

    const spawn = { tool: "subagent", sessionID: "s1", input: { prompt: "child brief", description: "d" } }
    await hooks.tool["execute.before"][0](spawn)
    assert.deepEqual(spawn.input, { prompt: "child brief + recall", description: "d" })

    const other = { tool: "read", sessionID: "s1", input: { path: "a.go" } }
    await hooks.tool["execute.before"][0](other)
    assert.deepEqual(other.input, { path: "a.go" })

    const failed = {
      tool: "shell",
      sessionID: "s1",
      input: { command: "go build ./..." },
      status: "completed",
      result: { content: [{ type: "text", text: "missing go.sum entry" }] },
    }
    await hooks.tool["execute.after"][0](failed)
    assert.deepEqual(failed.result.content, [
      { type: "text", text: "missing go.sum entry" },
      { type: "text", text: "ran next: go mod tidy" },
    ])

    const errored = { tool: "shell", sessionID: "s1", input: { command: "x" }, status: "error", error: {} }
    await hooks.tool["execute.after"][0](errored)
    assert.equal(errored.result, undefined)
  })
})

test("the switches in the plugin's options still hold on 2.x", async () => {
  await withHome(async (dir) => {
    const { ctx, hooks, tools } = fakeContext(dir, { bin: stubDeja(dir), tools: false, autoRecall: false })
    await plugin.setup(ctx)
    assert.equal(tools.length, 0)
    assert.equal(hooks.session.context, undefined)
    assert.equal(hooks.tool["execute.after"], undefined)
  })
})
