import { test } from "node:test"
import assert from "node:assert/strict"
import { accessNote, argv, configPath, contributions, installerPluginPath, mcpWired, promptText, toolCall, where } from "../lib.mjs"

test("a missing conversation-access grant is named with the command that sets it", () => {
  const granted = { plugins: { entries: { "deja-vu": { hooks: { allowConversationAccess: true } } } } }
  assert.equal(accessNote(granted, "deja-vu"), "")
  const missing = [
    undefined,
    {},
    { plugins: { entries: { "deja-vu": { enabled: true } } } },
    { plugins: { entries: { "deja-vu": { hooks: { allowConversationAccess: false } } } } },
    // The grant is per entry: another plugin's does not count.
    { plugins: { entries: { other: { hooks: { allowConversationAccess: true } } } } },
  ]
  for (const cfg of missing) {
    assert.match(accessNote(cfg, "deja-vu"), /openclaw config set plugins\.entries\.deja-vu\.hooks\.allowConversationAccess true/)
  }
})

test("a query that starts with a dash gets the flag terminator", () => {
  assert.deepEqual(argv("search", ["--limit", "5"], "--json"), ["search", "--limit", "5", "--", "--json"])
  assert.deepEqual(argv("search", ["--limit", "5"], "pgbouncer"), ["search", "--limit", "5", "pgbouncer"])
})

test("the installer's plugin and config are looked for where the installer writes them", () => {
  assert.equal(installerPluginPath({}, "/home/u"), "/home/u/.openclaw/extensions/deja/index.mjs")
  assert.equal(installerPluginPath({ OPENCLAW_STATE_DIR: "/srv/oc" }, "/home/u"), "/srv/oc/extensions/deja/index.mjs")
  assert.equal(configPath({}, "/home/u"), "/home/u/.openclaw/openclaw.json")
})

test("an MCP server the installer wrote means the tools are already there", () => {
  assert.equal(mcpWired('{"mcp":{"servers":{"deja":{"command":"deja","args":["mcp"]}}}}'), true)
  assert.equal(mcpWired('// comment\n{"mcp":{"servers":{"other":{}}}}'), false)
  assert.equal(mcpWired("not json"), false)
})

test("contributions fill the gaps and never repeat the installer", () => {
  assert.deepEqual(contributions({ mcp: false, recall: false }), { tools: true, recall: true })
  assert.deepEqual(contributions({ mcp: true, recall: true }), { tools: false, recall: false })
  assert.deepEqual(contributions({ mcp: false, recall: false }, { autoRecall: false }), { tools: true, recall: false })
})

test("the prompt and session key are read from the shapes the host sends", () => {
  assert.equal(promptText({ prompt: "  fix the pool  " }), "fix the pool")
  assert.equal(promptText({ prompt: ["a", "b"] }), "a\nb")
  assert.equal(promptText({}), "")
})

test("hooks name the transcript id and the agent's workspace, not the session key", () => {
  // hook-context stamps the session live under the id the index knows it by;
  // agent:main:main names no transcript (#4582).
  const seen = new Map()
  assert.deepEqual(where(seen, {}, { sessionKey: "agent:main:main", sessionId: "t1", workspaceDir: "/w" }, "/gw"), { id: "t1", cwd: "/w" })
  // The run's own compaction hands over only the key: what it last ran as stands in.
  assert.deepEqual(where(seen, {}, { sessionKey: "agent:main:main" }, "/gw"), { id: "t1", cwd: "/w" })
  assert.deepEqual(where(new Map(), { sessionId: "e1" }, {}, "/gw"), { id: "e1", cwd: "/gw" })
  assert.deepEqual(where(new Map(), {}, { sessionKey: "k" }, "/gw"), { id: "k", cwd: "/gw" })
})

test("the package wires every seam the installer's plugin does", async () => {
  const { default: plugin } = await import("../index.mjs")
  const events = []
  const prev = process.env.OPENCLAW_STATE_DIR
  process.env.OPENCLAW_STATE_DIR = "/nonexistent-openclaw-state"
  try {
    const api = { pluginConfig: { tools: false, bin: "/nonexistent/deja" }, on: (name) => events.push(name), registerTool() {} }
    plugin.register(api)
    plugin.register({ ...api, registerHook: (name) => events.push("hook " + name) })
    plugin.register({ ...api, config: { hooks: { internal: { enabled: false } } }, registerHook: (name) => events.push("hook " + name) })
  } finally {
    if (prev === undefined) delete process.env.OPENCLAW_STATE_DIR
    else process.env.OPENCLAW_STATE_DIR = prev
  }
  const seams = ["before_prompt_build", "before_compaction", "session_end"]
  // The digest rides agent:bootstrap where plugin hooks run, and the first
  // prompt otherwise.
  assert.deepEqual(events, [
    "agent_turn_prepare", ...seams,
    "hook agent:bootstrap", ...seams,
    "agent_turn_prepare", ...seams,
  ])
})

test("a finished tool maps to the call the installer's plugin makes", () => {
  const at = { id: "S1", cwd: "/w" }
  assert.deepEqual(toolCall({ toolName: "read", args: { path: "a.go" } }, at), {
    args: ["hook-tool", "--plain"],
    payload: { tool_name: "read", tool_input: { file_path: "a.go" }, session_id: "S1", cwd: "/w" },
  })
  assert.equal(toolCall({ toolName: "write", args: { file_path: "b.go" } }, at).payload.tool_name, "edit")
  const failed = toolCall({ toolName: "exec", args: { command: "make" }, result: { content: [{ type: "text", text: "boom" }] } }, at)
  assert.deepEqual(failed.args, ["hook-tool-after", "--plain"])
  assert.equal(failed.payload.tool_response, "boom")
  assert.equal(toolCall({ toolName: "exec", args: { command: "make" }, result: { content: [] } }, at), null)
  assert.equal(toolCall({ toolName: "web_search", args: {} }, at), null)
})
