import assert from "node:assert/strict"
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test from "node:test"

import {
  appendRecall,
  formatNotificationBody,
  installerOwns,
  isSubagentTool,
  orcaRoots,
  parseRecallArgs,
  resolveDeja,
  rewriteSubagentInput,
  userPromptHookPresent,
} from "../lib.mjs"
import { createWorker } from "../worker.mjs"

test("orca-plugin.json declares v2 trusted-worker contract cleanly", () => {
  const manifest = JSON.parse(readFileSync(new URL("../orca-plugin.json", import.meta.url), "utf8"))
  assert.equal(manifest.manifestVersion, 2)
  assert.equal(manifest.execution, "trusted-worker")
  assert.equal(manifest.id, "deja")
  assert.equal(manifest.main, "worker.mjs")
  assert.equal(manifest.pluginApi, 1)
  assert.equal(manifest.license, "MIT")
  assert.equal(manifest.publisher, undefined)

  for (const reserved of ["agentHarnesses", "skills", "mcpServers", "taskSources", "connections", "sidebarCards"]) {
    assert.equal(manifest[reserved], undefined, `reserved key ${reserved} must be omitted`)
  }
  assert.deepEqual(
    manifest.contributes.hooks.map((h) => h.event),
    ["agent.prompt.before", "agent.tool.before", "agent.tool.after"],
  )
  assert.ok(manifest.contributes.hooks.every((h) => h.onError === "pass"))
})

test("orcaRoots follows DEJA_ORCA_ROOT and OS defaults", () => {
  assert.deepEqual(orcaRoots({ DEJA_ORCA_ROOT: "/tmp/orca-test" }, "/home/alex", "darwin"), ["/tmp/orca-test"])
  assert.deepEqual(orcaRoots({}, "/Users/alex", "darwin"), ["/Users/alex/Library/Application Support/orca"])
  assert.deepEqual(orcaRoots({ XDG_CONFIG_HOME: "/home/alex/.cfg" }, "/home/alex", "linux"), [
    "/home/alex/.cfg/orca",
    "/home/alex/.cfg/Orca",
  ])
  assert.deepEqual(orcaRoots({ APPDATA: "C:\\Users\\alex\\AppData\\Roaming" }, "C:\\Users\\alex", "win32"), [
    join("C:\\Users\\alex\\AppData\\Roaming", "orca"),
    join("C:\\Users\\alex\\AppData\\Roaming", "Orca"),
  ])
})

test("installerOwns detects UserPromptSubmit deja hook in Orca settings.json", () => {
  const dir = mkdtempSync(join(tmpdir(), "deja-orca-"))
  try {
    const env = { DEJA_ORCA_ROOT: dir }
    assert.equal(installerOwns("hook-prompt", env, "/Users/alex", "darwin"), false)
    assert.equal(userPromptHookPresent("{broken json"), false)

    writeFileSync(
      join(dir, "settings.json"),
      JSON.stringify({
        hooks: {
          UserPromptSubmit: [{ hooks: [{ type: "command", command: "deja hook-prompt" }] }],
        },
      }),
    )
    assert.equal(installerOwns("hook-prompt", env, "/Users/alex", "darwin"), true)
    assert.equal(installerOwns("mcp", env, "/Users/alex", "darwin"), false)
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})

test("resolveDeja honors DEJA_BIN then well-known paths then PATH", () => {
  assert.equal(resolveDeja({ DEJA_BIN: "/custom/deja" }, "/Users/alex", () => true, "darwin"), "/custom/deja")
  assert.equal(
    resolveDeja({}, "/Users/alex", (p) => p === "/opt/homebrew/bin/deja", "darwin"),
    "/opt/homebrew/bin/deja",
  )
  assert.equal(resolveDeja({}, "/Users/alex", () => false, "darwin"), "deja")
  assert.equal(resolveDeja({}, "/Users/alex", () => false, "win32"), "deja.exe")
})

test("rewriteSubagentInput mutates only Agent/Task prompts", () => {
  const frame = '<deja-recall source="deja">past fix</deja-recall>'
  assert.equal(isSubagentTool("Agent"), true)
  assert.equal(isSubagentTool("Task"), true)
  assert.equal(isSubagentTool("Bash"), false)

  assert.equal(rewriteSubagentInput("Bash", { command: "go test ./...", prompt: "ignored" }, frame), null)
  assert.equal(rewriteSubagentInput("Edit", { file_path: "a.go", prompt: "ignored" }, frame), null)
  assert.equal(rewriteSubagentInput("Agent", { prompt: "" }, frame), null)

  const rewritten = rewriteSubagentInput("Agent", { prompt: "inspect worker.mjs", subagent_type: "code" }, frame)
  assert.deepEqual(rewritten, {
    prompt: `inspect worker.mjs\n\n${frame}`,
    subagent_type: "code",
  })
  assert.equal(appendRecall(`hello\n\n${frame}`, frame), `hello\n\n${frame}`)
})

test("worker resolves workspace cwd, injects recall, stands down, and runs commands", async () => {
  const hooks = new Map()
  const commands = new Map()
  const notifications = []
  let listCalls = 0
  let hookCalls = 0

  const orca = {
    hooks: {
      on(event, fn) {
        hooks.set(event, fn)
      },
    },
    commands: {
      register(id, fn) {
        commands.set(id, fn)
      },
    },
    host: {
      async call(method, params) {
        if (method === "workspace.readContext") return { workspaceId: "ws-1", terminals: ["t-1"] }
        if (method === "workspace.list") {
          listCalls++
          return { workspaces: [{ id: "ws-1", path: "/Users/alex/repo" }] }
        }
        if (method === "notifications.show") {
          notifications.push(params)
          return {}
        }
        throw new Error(`unexpected host call: ${method}`)
      },
    },
  }

  let owned = false
  const activate = createWorker({
    owns: () => owned,
    run: async (args, opts) => {
      hookCalls++
      if (args[0] === "hook-prompt") {
        const payload = JSON.parse(opts.stdin)
        assert.equal(payload.cwd, "/Users/alex/repo")
        return { ok: true, stdout: '<deja-recall source="deja">use node --test</deja-recall>\n' }
      }
      if (args[0] === "hook-context") {
        const payload = JSON.parse(opts.stdin)
        assert.equal(payload.cwd, "/Users/alex/repo")
        return { ok: true, stdout: "Recent work in repo" }
      }
      if (args[0] === "search") {
        assert.deepEqual(args, ["search", "--limit", "5", "--", "orca hook"])
        return { ok: true, stdout: "1 session matched orca hook" }
      }
      return { ok: true, stdout: "" }
    },
  })

  await activate(orca)

  const promptOut = await hooks.get("agent.prompt.before")(
    { agent: "claude", terminalId: "t-1", prompt: "fix test", inputKind: "text" },
    (p) => p,
  )
  assert.equal(promptOut.prompt, 'fix test\n\n<deja-recall source="deja">use node --test</deja-recall>')
  assert.equal(listCalls, 1)

  const bashOut = await hooks.get("agent.tool.before")(
    { agent: "claude", workspaceId: "ws-1", tool: "Bash", input: { command: "ls" } },
    (p) => p,
  )
  assert.deepEqual(bashOut.input, { command: "ls" })
  assert.equal(hookCalls, 1)

  const agentOut = await hooks.get("agent.tool.before")(
    { agent: "claude", workspaceId: "ws-1", tool: "Agent", input: { prompt: "check schema" } },
    (p) => p,
  )
  assert.equal(agentOut.input.prompt, 'check schema\n\n<deja-recall source="deja">use node --test</deja-recall>')
  assert.equal(hookCalls, 2)

  owned = true
  const stoodDown = await hooks.get("agent.prompt.before")(
    { agent: "claude", terminalId: "t-1", prompt: "fix test", inputKind: "text" },
    (p) => p,
  )
  assert.equal(stoodDown.prompt, "fix test")
  assert.equal(hookCalls, 2)

  const orientRes = await commands.get("deja.orient")({}, {})
  assert.equal(orientRes.text, "Recent work in repo")
  const recallRes = await commands.get("deja.recall")({ query: "orca hook" }, {})
  assert.equal(recallRes.text, "1 session matched orca hook")
  assert.equal(notifications.length, 2)
  assert.equal(parseRecallArgs(["orca", "hook"]), "orca hook")
  assert.equal(formatNotificationBody("a\n  b"), "a b")
})
