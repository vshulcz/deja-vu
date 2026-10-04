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

// The package lagged the plugin `deja install` writes by two whole channels: a
// spawned agent got no memory, and a failed command never surfaced the repair.
test("every channel the generated plugin has, the package has", async () => {
  const dir = mkdtempSync(join(tmpdir(), "deja-oc-"))
  await withConfigHome(dir, async () => {
    const hooks = await DejaPlugin({ client: quietClient(), directory: dir })
    for (const name of [
      "experimental.chat.system.transform",
      "experimental.chat.messages.transform",
      "experimental.session.compacting",
      "tool.execute.before",
      "tool.execute.after",
    ]) {
      assert.equal(typeof hooks[name], "function", `${name} is not wired`)
    }
  })
})

test("the spawn hook rewrites a subagent's prompt, and only a subagent's", () => {
  const source = readFileSync(new URL("../index.js", import.meta.url), "utf8")
  const at = source.indexOf('"tool.execute.before"')
  const body = source.slice(at, source.indexOf('"tool.execute.after"'))
  assert.ok(body.includes('input?.tool !== "task"'), "not scoped to the spawn tool")
  assert.ok(body.includes("hook-tool"), "does not call the pre-tool hook")
  assert.ok(body.includes("updatedInput?.prompt"), "does not read the rewritten prompt")
  assert.ok(body.includes("args.prompt = next"), "does not put the recall in the child's prompt")
})

test("the after-tool hook folds the fix line into a failed command's output", () => {
  const source = readFileSync(new URL("../index.js", import.meta.url), "utf8")
  const at = source.indexOf('"tool.execute.after"')
  const body = source.slice(at)
  assert.ok(body.includes('input?.tool !== "bash"'), "not scoped to bash")
  assert.ok(body.includes("hook-tool-after"), "does not call the failure hook")
  assert.ok(body.includes("tool_response: { output: text }"), "does not pass the command output")
  assert.ok(body.includes("output.output = text +"), "does not fold the line into the output")
})

test("the per-prompt hook carries the session id, so it does not repeat itself", () => {
  const source = readFileSync(new URL("../index.js", import.meta.url), "utf8")
  const at = source.indexOf('"experimental.chat.messages.transform"')
  const body = source.slice(at, source.indexOf('"experimental.session.compacting"'))
  assert.ok(body.includes("session_id: key"), "the per-prompt payload carries no session id")
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
  // opencode keeps a switched-off server in the file; switched off, it offers
  // nothing, so the package's tools are not a second copy (#3193).
  assert.equal(mcpWired(`{"mcp": {"deja": {"type": "local", "command": ["deja", "mcp"], "enabled": false}}}`), false)
  assert.equal(mcpWired(`{"mcp": {"deja": {"type": "local", "command": ["deja", "mcp"], "enabled": true}}}`), true)
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

// opencode sends every entry of output.system as its own system message, and a
// strict chat template (vLLM or SGLang serving Qwen) answers a second one with
// "System message must be at the beginning." (#4058)
test("the session digest joins the first system message instead of adding one", async () => {
  const dir = mkdtempSync(join(tmpdir(), "deja-oc-"))
  const bin = join(dir, "deja")
  writeFileSync(
    bin,
    `#!/bin/sh\nif [ "$1" = hook-context ]; then echo '{"hookSpecificOutput":{"additionalContext":"past work"}}'; else echo 0.0.0; fi\n`,
    { mode: 0o755 },
  )
  await withConfigHome(dir, async () => {
    const hooks = await DejaPlugin({ client: quietClient(), directory: dir }, { bin })
    const transform = hooks["experimental.chat.system.transform"]

    const output = { system: ["agent prompt"] }
    await transform({ sessionID: "s1" }, output)
    assert.deepEqual(output.system, ["agent prompt\n\npast work"])

    const empty = { system: [] }
    await transform({ sessionID: "s1" }, empty)
    assert.deepEqual(empty.system, ["past work"])
  })
})

// hook-context reads the session from stdin, as the plugin `deja install
// opencode-auto` writes sends it. Without it deja cannot tell the session it is
// answering from the rest, hands it its own session back and never reads the
// cached digest (#4273).
test("the session digest is asked for with the session id", async () => {
  const dir = mkdtempSync(join(tmpdir(), "deja-oc-"))
  const bin = join(dir, "deja")
  const log = join(dir, "stdin.log")
  writeFileSync(
    bin,
    `#!/bin/sh\nif [ "$1" = hook-context ]; then cat > '${log}'; echo '{}'; else echo 0.0.0; fi\n`,
    { mode: 0o755 },
  )
  await withConfigHome(dir, async () => {
    const hooks = await DejaPlugin({ client: quietClient(), directory: dir }, { bin })
    await hooks["experimental.chat.system.transform"]({ sessionID: "ses_abc123" }, { system: [] })
    assert.deepEqual(JSON.parse(readFileSync(log, "utf8")), { session_id: "ses_abc123", parent_session_id: "", cwd: dir })
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

// Everything deja says at the point of an action reached every harness except
// this one: both tool seams were scoped to a tool name, so a read produced
// nothing and the file's history never arrived. Measured on the fixture it was
// built for, the line at the read took a task from 19 tool calls to 7.
test("the after-tool hook carries a file's history back from a read", () => {
  const source = readFileSync(new URL("../index.js", import.meta.url), "utf8")
  const body = source.slice(source.indexOf('"tool.execute.after"'))
  assert.ok(body.includes("args?.filePath"), "does not read the path opencode passes")
  assert.ok(body.includes("hook-tool"), "does not call the point-of-action hook")
  assert.ok(
    body.indexOf("const fpath") < body.indexOf('if (input?.tool !== "bash") return'),
    "the file branch is behind the bash gate, so a read never reaches it",
  )
  assert.ok(body.includes("output.output = ftext"), "the line is not folded into the tool result")
})

// A session the plugin stamped live through hook-prompt was never ended, so it
// stayed out of the next session's MCP recall for twenty minutes (#4546).
// opencode publishes session.idle when a turn is over, and awaits dispose
// before `opencode run` exits.
test("the plugin ends the sessions it stamped, at idle and at dispose", async () => {
  const dir = mkdtempSync(join(tmpdir(), "deja-oc-"))
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
  await withConfigHome(dir, async () => {
    const hooks = await DejaPlugin({ client: quietClient(), directory: dir }, { bin })
    await hooks["experimental.chat.messages.transform"](
      { sessionID: "ses_F" },
      { messages: [{ info: { role: "user", sessionID: "ses_F" }, parts: [{ type: "text", text: "the retry loop" }] }] },
    )
    assert.equal(typeof hooks.event, "function", "no event hook: a stamped session is never ended")
    await hooks.event({ event: { type: "session.status", properties: { sessionID: "ses_F", status: { type: "busy" } } } })
    assert.equal(ended().length, 0, "a busy status ended the session")
    await hooks.event({ event: { type: "session.idle", properties: { sessionID: "ses_F" } } })
    assert.deepEqual(ended(), ['hook-session-end {"session_id":"ses_F"}'])
    await hooks["experimental.chat.messages.transform"](
      { sessionID: "ses_G" },
      { messages: [{ info: { role: "user", sessionID: "ses_G" }, parts: [{ type: "text", text: "the flaky test" }] }] },
    )
    rmSync(calls, { force: true })
    assert.equal(typeof hooks.dispose, "function", "no dispose: opencode run exits with the session stamped")
    await hooks.dispose()
    // idle already ended ses_F; dispose ends only what is still live.
    assert.deepEqual(ended(), ['hook-session-end {"session_id":"ses_G"}'])
  })
})

// A task sub-agent's digest and recall led with the session that spawned it,
// which is live and asking through it (#4548). opencode says who the parent is.
test("the plugin names a sub-agent's parent to the digest and the recall", async () => {
  const dir = mkdtempSync(join(tmpdir(), "deja-oc-"))
  const bin = join(dir, "deja")
  const calls = join(dir, "calls")
  writeFileSync(bin, `#!/bin/sh\nif [ "$1" = version ]; then echo 0.0.0; exit 0; fi\nprintf '%s %s\\n' "$1" "$(cat)" >> ${calls}\n`, {
    mode: 0o755,
  })
  const client = {
    ...quietClient(),
    session: { get: async ({ path }) => ({ data: { id: path.id, parentID: path.id === "ses_child" ? "ses_parent" : undefined } }) },
  }
  await withConfigHome(dir, async () => {
    const hooks = await DejaPlugin({ client, directory: dir }, { bin })
    await hooks["experimental.chat.system.transform"]({ sessionID: "ses_child" }, { system: [] })
    await hooks["experimental.chat.messages.transform"](
      { sessionID: "ses_child" },
      { messages: [{ info: { role: "user", sessionID: "ses_child" }, parts: [{ type: "text", text: "find the retry fix" }] }] },
    )
    const lines = readFileSync(calls, "utf8").split("\n")
    for (const hook of ["hook-context", "hook-prompt"]) {
      const line = lines.find((l) => l.startsWith(hook + " "))
      assert.ok(line, `${hook} was not called`)
      const payload = JSON.parse(line.slice(hook.length + 1))
      assert.equal(payload.session_id, "ses_child", hook)
      assert.equal(payload.parent_session_id, "ses_parent", hook)
    }
  })
})

// With the #4546 fix a session is ended at every session.idle and stamped live
// again by the next prompt's hook-prompt. A turn that is only an image has no
// text, and the transform returned before the stamp, so MCP recall in that
// turn could hand the session back to itself (#4573).
test("a prompt with no text still stamps the session live", async () => {
  const dir = mkdtempSync(join(tmpdir(), "deja-oc-"))
  const bin = join(dir, "deja")
  const calls = join(dir, "calls")
  writeFileSync(bin, `#!/bin/sh\nif [ "$1" = version ]; then echo 0.0.0; exit 0; fi\nprintf '%s %s\\n' "$1" "$(cat)" >> ${calls}\n`, {
    mode: 0o755,
  })
  await withConfigHome(dir, async () => {
    const hooks = await DejaPlugin({ client: quietClient(), directory: dir }, { bin })
    const image = { type: "file", mime: "image/png", url: "data:image/png;base64,AAAA" }
    const output = { messages: [{ info: { role: "user", sessionID: "ses_I" }, parts: [image] }] }
    await hooks["experimental.chat.messages.transform"]({ sessionID: "ses_I" }, output)
    const line = readFileSync(calls, "utf8").split("\n").find((l) => l.startsWith("hook-prompt "))
    assert.ok(line, "an image-only turn never reached hook-prompt, so the session was not stamped live")
    assert.equal(JSON.parse(line.slice("hook-prompt ".length)).session_id, "ses_I")
    assert.deepEqual(output.messages[0].parts, [image], "the image part was changed")
  })
})
