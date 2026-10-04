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
  // 2.x switches a server off with `disabled`, not `enabled`.
  assert.equal(mcpWired(`{"mcp": {"servers": {"deja": {"command": ["deja", "mcp"], "disabled": true}}}}`), false)
  assert.equal(mcpWired(`{"mcp": {"servers": {"deja": {"command": ["deja", "mcp"], "disabled": false}}}}`), true)
  assert.equal(mcpWired(`{"mcp": {"deja": {"command": ["deja", "mcp"], "disabled": true}}}`), false)
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
    // 2.x offers a tool to the model directly only when codemode is false;
    // otherwise it hides behind the code-mode `execute` tool.
    for (const t of tools) assert.deepEqual(t.options, { codemode: false }, `${t.name} is not a direct tool`)
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
    assert.equal(context.system[0].text, "agent prompt\n\npast work")
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

// deja can exit before reading its stdin (a missing index, an early refusal);
// the write then fails with EPIPE, and an unhandled error event on the pipe
// is an uncaught exception in opencode's process. CI hit it on this test file.
test("every write to deja's stdin has an error handler on the pipe", () => {
  for (const rel of ["../index.js", "../../openclaw/index.mjs"]) {
    const src = readFileSync(new URL(rel, import.meta.url), "utf8")
    const writes = src.match(/stdin\.(end|write)\(/g) || []
    const handlers = src.match(/stdin\.on\("error"/g) || []
    assert.ok(writes.length > 0, `${rel} writes nothing to stdin; the check is stale`)
    assert.ok(handlers.length > 0, `${rel} writes to deja's stdin with no error handler`)
  }
})

// 2.x has no event hook in the table: a plugin subscribes through ctx.event and
// returns a cleanup from setup. Without either, the 1.x session end never ran
// on 2.x (#4571). 2.0.22 ends a turn with session.execution.succeeded, .failed
// or .interrupted, and `opencode run` awaits the cleanup before it exits.
test("on 2.x the sessions it stamped end with the turn and at cleanup", async () => {
  await withHome(async (dir) => {
    const bin = join(dir, "deja")
    const calls = join(dir, "calls")
    writeFileSync(bin, `#!/bin/sh\nif [ "$1" = version ]; then echo 0.0.0; exit 0; fi\nprintf '%s %s\\n' "$1" "$(cat)" >> ${calls}\n`, {
      mode: 0o755,
    })
    const ended = () => {
      let text = ""
      try {
        text = readFileSync(calls, "utf8")
      } catch {}
      return text.split("\n").filter((l) => l.startsWith("hook-session-end "))
    }
    const { ctx, hooks } = fakeContext(dir, { bin })
    const events = channel()
    ctx.event = { subscribe: events.subscribe }
    const cleanup = await plugin.setup(ctx)
    assert.equal(typeof cleanup, "function", "setup returned no cleanup: opencode run exits with the session stamped")
    await hooks.session.context[0]({
      sessionID: "s1",
      system: [],
      messages: [{ role: "user", content: [{ type: "text", text: "the retry loop" }] }],
    })
    await events.push({ type: "session.execution.started", data: { sessionID: "s1" } })
    assert.equal(ended().length, 0, "a turn starting ended the session")
    await events.push({ type: "session.execution.succeeded", data: { sessionID: "s1" } })
    assert.deepEqual(ended(), ['hook-session-end {"session_id":"s1"}'])
    await hooks.session.context[0]({
      sessionID: "s2",
      system: [],
      messages: [{ role: "user", content: [{ type: "text", text: "the flaky test" }] }],
    })
    rmSync(calls, { force: true })
    await cleanup()
    // s1's turn already ended it; the cleanup ends only what is still live.
    assert.deepEqual(ended(), ['hook-session-end {"session_id":"s2"}'])
    assert.equal(events.open(), false, "the subscription outlived the plugin")
  })
})

// channel stands in for ctx.event.subscribe: push hands an event to the
// subscriber and waits for it to be handled.
function channel() {
  let waiting = null
  let handled = null
  let open = false
  return {
    open: () => open,
    subscribe: (options) => ({
      async *[Symbol.asyncIterator]() {
        open = true
        try {
          while (!options?.signal?.aborted) {
            const next = await new Promise((resolve) => {
              waiting = resolve
              options?.signal?.addEventListener("abort", () => resolve(null), { once: true })
            })
            if (!next) return
            yield next
            handled?.()
          }
        } finally {
          open = false
        }
      },
    }),
    push: async (event) => {
      while (!waiting) await new Promise((r) => setTimeout(r, 5))
      const done = new Promise((r) => (handled = r))
      const give = waiting
      waiting = null
      give(event)
      await Promise.race([done, new Promise((r) => setTimeout(r, 2000))])
      await new Promise((r) => setTimeout(r, 50))
    },
  }
}
