import { test } from "node:test"
import assert from "node:assert/strict"

import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"

import {
  argv,
  clampLimit,
  cliPluginPath,
  contextText,
  contributions,
  lastUserText,
  mcpWired,
  stripJSONComments,
} from "../lib.js"
import { DejaPlugin } from "../index.js"

test("contextText reads the hook shape deja prints", () => {
  const raw = JSON.stringify({
    systemMessage: "deja: recalled 3 prior sessions",
    hookSpecificOutput: { additionalContext: "past work" },
  })
  assert.deepEqual(contextText(raw), { context: "past work", receipt: "deja: recalled 3 prior sessions" })
})

test("contextText falls back to bare text", () => {
  assert.deepEqual(contextText("  past work  "), { context: "past work", receipt: "" })
  assert.deepEqual(contextText(""), { context: "", receipt: "" })
})

test("lastUserText joins the newest user message, ignoring the assistant's", () => {
  const messages = [
    { info: { role: "user" }, parts: [{ type: "text", text: "old" }] },
    { info: { role: "assistant" }, parts: [{ type: "text", text: "reply" }] },
    { info: { role: "user" }, parts: [{ type: "text", text: "why" }, { type: "text", text: "this" }] },
  ]
  const { prompt, parts } = lastUserText(messages)
  assert.equal(prompt, "why\nthis")
  assert.equal(parts.length, 2)
})

test("lastUserText is empty when there is nothing to rank against", () => {
  assert.equal(lastUserText([]).prompt, "")
  assert.equal(lastUserText(undefined).prompt, "")
  assert.equal(lastUserText([{ info: { role: "user" }, parts: [{ type: "file" }] }]).prompt, "")
})

test("clampLimit keeps the window small", () => {
  assert.equal(clampLimit(undefined), 5)
  assert.equal(clampLimit("3"), 3)
  assert.equal(clampLimit(0), 1)
  assert.equal(clampLimit(500), 20)
  assert.equal(clampLimit(NaN), 5)
})

// The plugin reads the installer's own config home, so the tests point it at a
// temporary one rather than at the machine's.
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

const quietClient = () => ({ tui: { showToast: async () => {} } })

test("cliPluginPath follows the installer's own config home", () => {
  assert.equal(
    cliPluginPath({ XDG_CONFIG_HOME: "/cfg" }, "/home/x"),
    join("/cfg", "opencode", "plugins", "deja.js"),
  )
  assert.equal(
    cliPluginPath({}, "/home/x"),
    join("/home/x", ".config", "opencode", "plugins", "deja.js"),
  )
})

// opencode resolves both installs into one plugin list — verified with
// `opencode debug config` against a config home holding both — and each pushed
// the same recall onto the system prompt and raised the same toast.
test("the plugin stands down when the installer's own plugin is on disk", async () => {
  const dir = mkdtempSync(join(tmpdir(), "deja-oc-"))
  mkdirSync(join(dir, "opencode", "plugins"), { recursive: true })
  writeFileSync(join(dir, "opencode", "plugins", "deja.js"), "// installed by deja\n")
  await withConfigHome(dir, async () => {
    const hooks = await DejaPlugin({ client: quietClient(), directory: dir })
    assert.equal(hooks["experimental.chat.system.transform"], undefined)
    assert.equal(hooks["experimental.chat.messages.transform"], undefined)
  })
})

test("without it the recall hooks are installed", async () => {
  const dir = mkdtempSync(join(tmpdir(), "deja-oc-"))
  await withConfigHome(dir, async () => {
    const hooks = await DejaPlugin({ client: quietClient(), directory: dir })
    assert.equal(typeof hooks["experimental.chat.system.transform"], "function")
    assert.equal(typeof hooks["experimental.chat.messages.transform"], "function")
  })
})

test("mcpWired reads the entry the installer writes, comments and all", () => {
  const config = `{
  // deja was wired by \`deja install opencode\`
  "mcp": {
    "deja": {"type":"local","command":["/usr/local/bin/deja","mcp"]}
  }
}`
  assert.equal(mcpWired(config), true)
  assert.equal(mcpWired(`{"mcp": {"other": {}}}`), false)
  assert.equal(mcpWired("{}"), false)
  assert.equal(mcpWired("not json"), false)
  assert.equal(mcpWired(""), false)
})

test("stripJSONComments leaves what is inside strings alone", () => {
  assert.equal(stripJSONComments(`{"a":"http://x"} // tail`).trim(), `{"a":"http://x"}`)
  assert.equal(stripJSONComments(`{/* off */"a":1}`), `{"a":1}`)
  assert.equal(stripJSONComments(`{"a":"say \\"//\\" here"}`), `{"a":"say \\"//\\" here"}`)
})

test("an MCP server in the config leaves recall as this package's job", async () => {
  const dir = mkdtempSync(join(tmpdir(), "deja-oc-"))
  mkdirSync(join(dir, "opencode"), { recursive: true })
  writeFileSync(
    join(dir, "opencode", "opencode.json"),
    JSON.stringify({ mcp: { deja: { type: "local", command: ["deja", "mcp"] } } }),
  )
  await withConfigHome(dir, async () => {
    const hooks = await DejaPlugin({ client: quietClient(), directory: dir })
    assert.equal(typeof hooks["experimental.chat.system.transform"], "function")
  })
})

// The registration decision itself, since the hook tests above cannot see the
// tools: registering those needs opencode's own plugin package, which the host
// provides and the test environment does not.
test("contributions fills the gaps and never repeats the installer", () => {
  assert.deepEqual(contributions({}, {}), { tools: true, recall: true })
  assert.deepEqual(contributions({ mcp: true }, {}), { tools: false, recall: true })
  assert.deepEqual(contributions({ recall: true }, {}), { tools: true, recall: false })
  assert.deepEqual(contributions({ mcp: true, recall: true }, {}), { tools: false, recall: false })
  // The user's own switches still win over an empty machine.
  assert.deepEqual(contributions({}, { tools: false }), { tools: false, recall: true })
  assert.deepEqual(contributions({}, { autoRecall: false }), { tools: true, recall: false })
  assert.deepEqual(contributions(undefined, {}), { tools: true, recall: true })
})

// deja's flag parser reads a query that starts with a dash as a flag, exits,
// and the plugin turns the failed call into "" — which the model receives as
// "nothing in this machine's history", while the sessions that discuss the flag
// sit right there.
test("a query that starts with a dash is not read as a flag", () => {
  assert.deepEqual(argv("search", [], "--no-verify"), ["search", "--", "--no-verify"])
  assert.deepEqual(argv("fix", [], "-race detected"), ["fix", "--", "-race detected"])
  // The plugin's own flags stay ahead of the terminator, or deja reads them as
  // part of the query.
  assert.deepEqual(argv("search", ["--json", "--limit", "5"], "--all-matches"), [
    "search",
    "--json",
    "--limit",
    "5",
    "--",
    "--all-matches",
  ])
})

test("the terminator is sent only when the query needs it", () => {
  // A deja too old to know `--` on this subcommand would otherwise fail on
  // every ordinary query, not just the ones that start with a dash.
  assert.deepEqual(argv("search", ["--json"], "the checkout worker"), [
    "search",
    "--json",
    "the checkout worker",
  ])
  assert.deepEqual(argv("blame", ["--json"], "internal/index/sync.go"), [
    "blame",
    "--json",
    "internal/index/sync.go",
  ])
})

test("every query the plugin sends goes through argv", () => {
  // The rule is worth nothing if one call site passes its text straight
  // through, which is how this got in.
  const source = readFileSync(new URL("../index.js", import.meta.url), "utf8")
  for (const call of source.match(/ask\(\[[^\]]*\]/g) || []) {
    assert.doesNotMatch(call, /String\(args\./, `${call} passes a query to deja without argv()`)
  }
})

// An empty answer is cached so the plugin does not shell out every turn. Its
// reasons are not alike: no history is permanent, a locked index or a call that
// did not get through is over by the next turn. Cached alike, one bad moment
// cost the session all of its memory — driven against the installed plugin, a
// single failed first call left turns two and three silent too.
test("an empty answer is not cached for the life of the session", () => {
  const source = readFileSync(new URL("../index.js", import.meta.url), "utf8")
  const compact = source.replace(/\s+/g, "")
  assert.match(compact, /constemptyRetries=\d/, "no bound on how often it asks again")
  assert.match(
    compact,
    /if\(asks<emptyRetries\)digests\.delete\(key\)/,
    "the cached emptiness is never dropped, so the session cannot recover",
  )
  // And the counter has to be per session, or one session's bad moment spends
  // another's retries.
  assert.match(compact, /empties\.set\(key,asks\)/, "the count is not kept per session")
})
