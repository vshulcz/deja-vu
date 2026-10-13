// deja's lines for the person, end to end through the plugin against a stand-in
// deja. Claude Code shows a hook's systemMessage; dsh shows a person nothing a
// plugin hands the model, so the plugin used to ask for --plain and the receipt
// never reached anyone.

import assert from "node:assert/strict";
import { chmodSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { newestEvents, noteBoard, splitHookReply } from "../lib.js";

const RECALL = "<deja-recall>\nthe staging queue drains with --grace 40\n</deja-recall>";
const RECEIPT = 'deja-vu — you have been here: "staging queue drain" (3 days ago · via: grace)';
const DIGEST = "<deja-recall>\nthis project settled on pnpm\n</deja-recall>";
const BUILT = "deja indexed 12 sessions (2 agents)";

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

test("a stray line ahead of the envelope does not carry the person's line to the model", () => {
  const reply = JSON.stringify({ systemMessage: RECEIPT, hookSpecificOutput: { additionalContext: RECALL } });
  assert.deepEqual(splitHookReply("warning: something\n" + reply), { context: RECALL, notes: RECEIPT });
  assert.deepEqual(splitHookReply('{"unrelated":1}\n' + reply), { context: RECALL, notes: RECEIPT });
  // No envelope anywhere: all of it is the model's, as before.
  assert.deepEqual(splitHookReply('{"unrelated":1}'), { context: '{"unrelated":1}', notes: "" });
});

test("the board hands a session's lines over once and stays bounded", () => {
  let clock = 0;
  const board = noteBoard({ perSession: 3, sessions: 2, ttlMs: 1000, now: () => clock });
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
  // A session reopened later does not open on an old receipt.
  board.push("e", "stale");
  clock = 5000;
  board.push("e", "fresh");
  assert.deepEqual(board.take("e"), ["fresh"]);
  assert.equal(board.hasReader(), false);
  board.setReader();
  assert.equal(board.hasReader(), true);
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
// envelope otherwise, and writes each call down. The plugin resolves deja once
// at import, so DEJA_BIN is set before the dynamic imports below.
const STAND = mkdtempSync(join(tmpdir(), "deja-fake-"));
const CALLS = join(STAND, "calls");
const BIN = join(STAND, "deja");
writeFileSync(
  BIN,
  `#!/usr/bin/env node
const fs = require("node:fs");
const args = process.argv.slice(2);
if (args[0] === "version") { console.log("deja 0.0.0-test"); process.exit(0); }
fs.appendFileSync(${JSON.stringify(CALLS)}, args.join(" ") + "\\n");
if (args[0] === "search") { console.log("HIT"); process.exit(0); }
if (args.includes("--notes")) process.exit(0);
const plain = args.includes("--plain");
const ctx = args[0] === "hook-prompt" ? ${JSON.stringify(RECALL)} : ${JSON.stringify(DIGEST)};
const note = args[0] === "hook-prompt" ? ${JSON.stringify(RECEIPT)} : ${JSON.stringify(BUILT)};
if (plain) { console.log(ctx); process.exit(0); }
console.log(JSON.stringify({ systemMessage: note, hookSpecificOutput: { hookEventName: "X", additionalContext: ctx } }));
`,
);
chmodSync(BIN, 0o755);
process.env.DEJA_BIN = BIN;
process.env.DSH_HOME = STAND;

const calls = () => {
  try {
    return readFileSync(CALLS, "utf8").trim().split("\n").filter(Boolean);
  } catch {
    return [];
  }
};

// A host with the given services: inject runs its callback only when every
// service it names is present, as Cordis does.
function host(services) {
  const seen = { contexts: new Map(), route: null, command: null };
  const all = {
    connection: { fetch: { register: (r) => (seen.route = r) } },
    webServer: {},
  };
  seen.ctx = {
    tools: { register() {} },
    commands: { register: (c) => (seen.command = c) },
    systemPrompt: { context: (c) => seen.contexts.set(c.name, c.text) },
    inject(deps, cb) {
      if (deps.every((d) => services.includes(d))) cb(Object.fromEntries(deps.map((d) => [d, all[d]])));
    },
  };
  return seen;
}

// dsh 0.2.0's session: the log is private and read with eventAt(seq). At
// assembly the prompt is only in the inbox splice that carried it in.
function assembly(sid) {
  const log = [
    { type: "agent/inbox/spliced", data: { inserted: [{ role: "user", source: { kind: "user" }, content: [{ type: "text", text: "how does the staging queue drain" }] }] } },
    { type: "turn/start", data: { turn: 1 } },
    { type: "agent/inbox/spliced", data: { removedCount: 1, inserted: [] } },
  ];
  return { agent: { sessionId: sid, session: { header: { cwd: STAND }, get seq() { return log.length; }, eventAt: (i) => log[i] } } };
}

const skip = process.platform === "win32";

test("the recall reaches the model unchanged and the receipt reaches the page", { skip }, async () => {
  const { default: apply } = await import("../index.js?web");
  const web = host(["connection", "webServer"]);
  apply(web.ctx, {});
  assert.ok(web.route, "no route for the page to ask");
  assert.equal(web.route.path, "/api/deja-notes");

  const before = calls().length;
  assert.equal(web.contexts.get("deja:project")(assembly("session-1234")), DIGEST, "the digest the model reads changed");
  assert.equal(web.contexts.get("deja:recall")(assembly("session-1234")), RECALL, "the recall the model reads changed");
  assert.deepEqual(calls().slice(before), ["hook-context", "hook-prompt"], "one call each, in the JSON form");

  const ask = async (session) =>
    (await (await web.route.fetch(new Request(`http://127.0.0.1/api/deja-notes?session=${session}`))).json()).notes;
  // The page names the session as dsh does; the board keys it as deja does.
  assert.deepEqual(await ask("session-1234"), [BUILT, RECEIPT]);
  assert.deepEqual(await ask("session-1234"), [], "a line was shown twice");
  assert.deepEqual(await ask("other"), []);

  // A web user who runs /deja before the page asked sees the lines there, once.
  web.contexts.get("deja:project")(assembly("session-5678"));
  const reply = await web.command.handler({ rawInput: "staging queue", agent: { sessionId: "session-5678" } });
  assert.ok(reply.text.includes(BUILT), reply.text);
  assert.deepEqual(await ask("session-5678"), [], "the lines /deja showed came back on the page");
});

// deja marks its notes shown when it prints them. A profile with no page —
// tui, headless, sdk — that asked for them anyway spent them where nobody
// looks: /deja lost them, and so did the next Claude Code session.
test("without a page deja is asked for --plain and keeps its notes", { skip }, async () => {
  const { default: apply } = await import("../index.js?tui");
  // tui has the connection service and no web server.
  const tui = host(["connection"]);
  apply(tui.ctx, {});
  assert.equal(tui.route, null, "a route nobody can reach");

  const before = calls().length;
  assert.equal(tui.contexts.get("deja:project")(assembly("session-9")), DIGEST);
  assert.equal(tui.contexts.get("deja:recall")(assembly("session-9")), RECALL);
  assert.deepEqual(calls().slice(before), ["hook-context --plain", "hook-prompt --plain"]);
});

