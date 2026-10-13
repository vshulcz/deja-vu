// deja's lines for the person, end to end through the plugin against a stand-in
// deja. Claude Code shows a hook's systemMessage; dsh shows a person nothing a
// plugin hands the model, so the plugin used to ask for --plain and the receipt
// never reached anyone.

import assert from "node:assert/strict";
import { chmodSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { newestEvents, noteBoard, splitHookReply } from "../lib.js";

const RECALL = "<deja-recall>\nthe staging queue drains with --grace 40\n</deja-recall>";
const RECEIPT = 'deja-vu — you have been here: "staging queue drain" (3 days ago · via: grace)';
const DIGEST = "<deja-recall>\nthis project settled on pnpm\n</deja-recall>";
const BUILT = "deja indexed 12 sessions from 2 agents";

test("the JSON reply splits into the model's text and the person's", () => {
  const reply = JSON.stringify({
    systemMessage: RECEIPT,
    hookSpecificOutput: { hookEventName: "UserPromptSubmit", additionalContext: RECALL },
  });
  assert.deepEqual(splitHookReply(reply + "\n"), { context: RECALL, notes: RECEIPT });
  // Context with no line for the person, and a line with no context.
  assert.deepEqual(
    splitHookReply(JSON.stringify({ hookSpecificOutput: { additionalContext: RECALL } })),
    { context: RECALL, notes: "" },
  );
  assert.deepEqual(
    splitHookReply(JSON.stringify({ systemMessage: BUILT, hookSpecificOutput: { additionalContext: "" } })),
    { context: "", notes: BUILT },
  );
  // Silence, and text that is not the envelope, which stays the model's.
  assert.deepEqual(splitHookReply(""), { context: "", notes: "" });
  assert.deepEqual(splitHookReply(RECALL), { context: RECALL, notes: "" });
  assert.deepEqual(splitHookReply("{not json"), { context: "{not json", notes: "" });
});

test("the board hands a session's lines over once and stays bounded", () => {
  const board = noteBoard(3, 2);
  board.push("a", "one\n\ntwo");
  board.push("a", "three\nfour");
  assert.deepEqual(board.take("a"), ["two", "three", "four"]);
  assert.deepEqual(board.take("a"), []);
  board.push("", "no session, nowhere to show it");
  board.push("b", "b");
  board.push("c", "c");
  board.push("d", "d");
  assert.deepEqual(board.take("b"), [], "the oldest session is dropped past the cap");
  assert.deepEqual(board.take("d"), ["d"]);
});

// dsh 0.2.0 dropped session.events for eventAt(seq); reading only the array
// found no prompt there, and the per-prompt recall never ran.
test("the session log is read newest first in either shape", () => {
  const log = ["a", "b", "c"];
  assert.deepEqual([...newestEvents({ events: log })], ["c", "b", "a"]);
  assert.deepEqual([...newestEvents({ seq: 3, eventAt: (i) => log[i] })], ["c", "b", "a"]);
  assert.deepEqual([...newestEvents({ seq: 3, eventAt: () => { throw new Error("gone"); } })], []);
  assert.deepEqual([...newestEvents({})], []);
  assert.deepEqual([...newestEvents(undefined)], []);
});

// The stand-in prints what deja prints: the bare digest for --plain, the hook
// envelope otherwise. The plugin resolves deja once at import, so DEJA_BIN is
// set before the dynamic import.
function fakeDeja() {
  const dir = mkdtempSync(join(tmpdir(), "deja-fake-"));
  const bin = join(dir, "deja");
  writeFileSync(
    bin,
    `#!/usr/bin/env node
const args = process.argv.slice(2);
if (args[0] === "version") { console.log("deja 0.0.0-test"); process.exit(0); }
const plain = args.includes("--plain");
const ctx = args[0] === "hook-prompt" ? ${JSON.stringify(RECALL)} : ${JSON.stringify(DIGEST)};
const note = args[0] === "hook-prompt" ? ${JSON.stringify(RECEIPT)} : ${JSON.stringify(BUILT)};
if (plain) { console.log(ctx); process.exit(0); }
console.log(JSON.stringify({ systemMessage: note, hookSpecificOutput: { hookEventName: "X", additionalContext: ctx } }));
`,
  );
  chmodSync(bin, 0o755);
  return { bin, dir };
}

test("the recall reaches the model unchanged and the receipt reaches the page", { skip: process.platform === "win32" }, async () => {
  const { bin, dir } = fakeDeja();
  process.env.DEJA_BIN = bin;
  process.env.DSH_HOME = dir;
  const { default: apply } = await import("../index.js?notes");

  const contexts = new Map();
  let route = null;
  const ctx = {
    tools: { register() {} },
    commands: { register() {} },
    systemPrompt: { context: (c) => contexts.set(c.name, c.text) },
    inject(deps, cb) {
      if (deps.includes("connection")) cb({ connection: { fetch: { register: (r) => (route = r) } } });
    },
  };
  apply(ctx, {});
  assert.ok(route, "no route for the page to ask");
  assert.equal(route.path, "/api/deja-notes");

  // dsh 0.2.0's session: the log is private and read with eventAt(seq). At
  // assembly the prompt is only in the inbox splice that carried it in.
  const log = [
    { type: "agent/inbox/spliced", data: { inserted: [{ role: "user", source: { kind: "user" }, content: [{ type: "text", text: "how does the staging queue drain" }] }] } },
    { type: "turn/start", data: { turn: 1 } },
    { type: "agent/inbox/spliced", data: { removedCount: 1, inserted: [] } },
  ];
  const assembly = {
    agent: {
      sessionId: "session-1234",
      session: { header: { cwd: dir }, get seq() { return log.length; }, eventAt: (i) => log[i] },
    },
  };
  assert.equal(contexts.get("deja:project")(assembly), DIGEST, "the digest the model reads changed");
  assert.equal(contexts.get("deja:recall")(assembly), RECALL, "the recall the model reads changed");

  const ask = async (session) =>
    (await (await route.fetch(new Request(`http://127.0.0.1/api/deja-notes?session=${session}`))).json()).notes;
  // The page names the session as dsh does; the board keys it as deja does.
  assert.deepEqual(await ask("session-1234"), [BUILT, RECEIPT]);
  assert.deepEqual(await ask("session-1234"), [], "a line was shown twice");
  assert.deepEqual(await ask("other"), []);
});
