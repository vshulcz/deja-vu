import { test } from "node:test"
import assert from "node:assert/strict"
import { argv, configPath, contributions, installerPluginPath, mcpWired, promptText, sessionKey } from "../lib.mjs"

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
  assert.equal(sessionKey({ sessionId: "e1" }, { sessionKey: "c1" }), "c1")
  assert.equal(sessionKey({ session: { id: "e2" } }, {}), "e2")
  assert.equal(sessionKey({}, {}), "")
})
