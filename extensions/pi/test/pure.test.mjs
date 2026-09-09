import { test } from "node:test"
import assert from "node:assert/strict"
import { argv, contextText, installerExtensionPath, installerExtensionPaths, sessionKey } from "../lib.mjs"

test("the installer's extension is looked for where the installer writes it", () => {
  assert.equal(installerExtensionPath("/home/u"), "/home/u/.pi/agent/extensions/deja.ts")
})

test("hook-context is read in both shapes deja prints", () => {
  assert.deepEqual(contextText('{"hookSpecificOutput":{"additionalContext":"digest"},"systemMessage":"deja recalled 2"}'), { context: "digest", receipt: "deja recalled 2" })
  assert.deepEqual(contextText("bare text"), { context: "bare text", receipt: "" })
  assert.deepEqual(contextText(""), { context: "", receipt: "" })
})

test("a query that starts with a dash gets the flag terminator", () => {
  assert.deepEqual(argv("search", [], "--json"), ["search", "--", "--json"])
  assert.deepEqual(argv("search", [], "pgbouncer"), ["search", "pgbouncer"])
})

test("the session key is what pi keeps on the session manager", () => {
  // pi 0.73 / omp 18: the event carries prompt and images only, the context a
  // sessionManager — the shapes the installer's extension reads (#3179).
  assert.equal(sessionKey({ prompt: "why" }, { sessionManager: { getSessionId: () => "s1" } }), "s1")
  assert.equal(sessionKey({ prompt: "why" }, { sessionManager: { sessionId: "s2" } }), "s2")
  assert.equal(sessionKey({ sessionId: "e1" }, { session: { id: "c1" } }), "e1")
  assert.equal(sessionKey({}, { session: { id: "c1" } }), "c1")
  assert.equal(sessionKey({}, {}), "")
})

test("the package stands down for either host's installed extension", () => {
  // omp runs this same package, and `deja install omp-auto` writes its
  // extension elsewhere — checking pi's path alone left both running, so the
  // recall went in twice and /deja was registered twice (#3180).
  assert.deepEqual(installerExtensionPaths("/home/u"), [
    "/home/u/.pi/agent/extensions/deja.ts",
    "/home/u/.omp/agent/extensions/deja/index.js",
  ])
})
