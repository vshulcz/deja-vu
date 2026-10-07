// The entry opencode loads as `opencode-deja/server`.
//
// opencode 2.x imports only a default export and requires `{ id, setup }` (or
// `effect`) on it; the named export in index.js fails its check with "Plugin
// must export a default definition with an id and an effect or setup
// function" (#4151). 1.x from 1.3.4 resolves the same `./server` subpath and
// wants `{ server }` on the default export, so this object carries both. 1.x
// before 1.3.4 never looks here and keeps loading index.js, which calls every
// export as a plugin and would choke on an object.
//
// setup runs the 1.x plugin and wires its hooks to 2.x's seams, so both majors
// run the same code: the session digest and the per-prompt recall go in on
// `session` "context", the pre-compaction index on "compaction", the spawn and
// after-tool lines on `tool` "execute.before" and "execute.after", the end of
// a turn on `event` "subscribe" and dispose on the cleanup setup returns.

import { DejaPlugin } from "./index.js"
import { jsonSchema, resultText, TOOL_SPECS, turnEnded, v1Client, v1Messages, v1ToolName } from "./lib.js"

async function setup(ctx) {
  const directory = ctx.location?.directory || process.cwd()
  const hooks = await DejaPlugin({ client: v1Client(ctx), directory }, ctx.options || {})

  const specs = hooks[TOOL_SPECS]
  if (specs) {
    await ctx.tool.transform((tools) => {
      for (const [name, spec] of Object.entries(specs)) {
        tools.add({
          name,
          // Only a codemode: false tool is offered to the model directly; the
          // rest sit behind the code-mode `execute` tool, as built-ins do not.
          options: { codemode: false },
          description: spec.description,
          input: jsonSchema(spec.args),
          execute: async (input) => ({ content: await spec.execute(input || {}) }),
        })
      }
    })
  }

  const system = hooks["experimental.chat.system.transform"]
  const prompt = hooks["experimental.chat.messages.transform"]
  if (system || prompt) {
    await ctx.session.hook("context", async (event) => {
      try {
        if (system) {
          const out = { system: event.system.map((p) => p.text) }
          await system({ sessionID: event.sessionID }, out)
          if (event.system.length) {
            if (out.system[0] !== event.system[0].text) event.system[0] = { ...event.system[0], text: out.system[0] }
          } else if (out.system.length) {
            event.system.push({ type: "text", text: out.system[0] })
          }
        }
        if (prompt) {
          await prompt({ sessionID: event.sessionID }, { messages: v1Messages(event.messages, event.sessionID) })
        }
      } catch {
        // memory is optional: never break the session over it
      }
    })
  }

  const compacting = hooks["experimental.session.compacting"]
  if (compacting) {
    // The 1.x hook reads the session from its input; called with none, 2.x
    // sent hook-precompact an empty id and nothing was read or forgotten.
    await ctx.session.hook("compaction", async (event) => {
      await compacting({ sessionID: event?.sessionID || "" })
    })
  }

  const before = hooks["tool.execute.before"]
  if (before) {
    await ctx.tool.hook("execute.before", async (event) => {
      const args = { ...(event.input || {}) }
      await before({ tool: v1ToolName(event.tool), sessionID: event.sessionID }, { args })
      if (args.prompt !== event.input?.prompt) event.input = args
    })
  }

  const after = hooks["tool.execute.after"]
  if (after) {
    await ctx.tool.hook("execute.after", async (event) => {
      if (event.status !== "completed") return
      const text = resultText(event.result)
      const output = { output: text }
      await after({ tool: v1ToolName(event.tool), sessionID: event.sessionID, args: event.input || {} }, output)
      if (output.output === text || !output.output.startsWith(text)) return
      const extra = output.output.slice(text.length).trim()
      const result = event.result || {}
      const content = Array.isArray(result.content)
        ? result.content
        : typeof result.content === "string"
          ? [{ type: "text", text: result.content }]
          : text
            ? [{ type: "text", text }]
            : []
      event.result = { ...result, content: [...content, { type: "text", text: extra }] }
    })
  }

  // 2.x has no event hook or dispose in a plugin's table: the events come from
  // ctx.event and the exit is the cleanup setup returns, which `opencode run`
  // awaits. Without them a session stamped live was never ended (#4571).
  const stop = new AbortController()
  if (hooks.event && typeof ctx.event?.subscribe === "function") {
    ;(async () => {
      for await (const event of ctx.event.subscribe({ signal: stop.signal })) {
        const id = turnEnded(event)
        if (id) await hooks.event({ event: { type: "session.idle", properties: { sessionID: id } } })
      }
    })().catch(() => {})
  }
  return async () => {
    stop.abort()
    await hooks.dispose?.()
  }
}

export default { id: "opencode-deja", setup, server: DejaPlugin }
