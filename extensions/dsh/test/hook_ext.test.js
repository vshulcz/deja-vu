import assert from "node:assert/strict";
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

const skip = process.platform === "win32";

function makeExtStub(mode) {
  const dir = mkdtempSync(join(tmpdir(), "deja-dsh-ext-"));
  const bin = join(dir, "deja");
  const calls = join(dir, "calls.log");
  writeFileSync(
    bin,
    `#!/usr/bin/env node
const fs = require("node:fs");
const args = process.argv.slice(2);
if (args[0] === "version") { console.log("deja 0.0.0-test"); process.exit(0); }
const needsStdin = args[0] === "hook-ext" || (args[0] && args[0].startsWith("hook-"));
let input = "";
if (needsStdin) {
  try { input = fs.readFileSync(0, "utf8"); } catch {}
}
fs.appendFileSync(${JSON.stringify(calls)}, JSON.stringify({ args, input }) + "\\n");
if (args[0] === "hook-ext") {
  const op = args[args.indexOf("--op") + 1] || "";
  const req = input ? JSON.parse(input) : {};
  if (${JSON.stringify(mode)} === "deny") {
    process.stdout.write(JSON.stringify({ ok: false, kind: "error", error: "blocked by policy" }) + "\\n");
    process.exit(1);
  }
  if (${JSON.stringify(mode)} === "legacy") {
    process.stderr.write("unknown command hook-ext\\n");
    process.exit(2);
  }
  if (op === "context") {
    process.stdout.write(JSON.stringify({ ok: true, kind: "hook", context: "EXT-CTX", note: "EXT-NOTE-CTX" }) + "\\n");
  } else if (op === "prompt") {
    process.stdout.write(JSON.stringify({ ok: true, kind: "hook", context: "EXT-PROMPT", note: "EXT-NOTE-PROMPT" }) + "\\n");
  } else if (op === "slash") {
    process.stdout.write(JSON.stringify({ ok: true, kind: "slash", output: "EXT-SLASH:" + (req.args || "") }) + "\\n");
  } else if (op === "tool-before") {
    process.stdout.write(JSON.stringify({ ok: true, kind: "hook", context: "EXT-TOOL-BEFORE" }) + "\\n");
  } else if (op === "tool-after") {
    process.stdout.write(JSON.stringify({ ok: true, kind: "hook", context: "EXT-TOOL-AFTER" }) + "\\n");
  } else {
    process.stdout.write(JSON.stringify({ ok: true, kind: "ok" }) + "\\n");
  }
  process.exit(0);
}
if (args[0] === "hook-context") {
  process.stdout.write(JSON.stringify({ systemMessage: "LEGACY-NOTE-CTX", hookSpecificOutput: { additionalContext: "LEGACY-CTX" } }) + "\\n");
  process.exit(0);
}
if (args[0] === "hook-prompt") {
  process.stdout.write(JSON.stringify({ systemMessage: "LEGACY-NOTE-PROMPT", hookSpecificOutput: { additionalContext: "LEGACY-PROMPT" } }) + "\\n");
  process.exit(0);
}
if (args[0] === "hook-tool") {
  process.stdout.write("LEGACY-TOOL-BEFORE\\n");
  process.exit(0);
}
if (args[0] === "hook-tool-after") {
  process.stdout.write("LEGACY-TOOL-AFTER\\n");
  process.exit(0);
}
if (args[0] === "search") {
  process.stdout.write("LEGACY-SLASH\\n");
  process.exit(0);
}
process.exit(0);
`,
  );
  chmodSync(bin, 0o755);
  const logged = () => {
    try {
      return readFileSync(calls, "utf8")
        .split("\n")
        .filter(Boolean)
        .map((line) => JSON.parse(line));
    } catch {
      return [];
    }
  };
  return { dir, bin, logged, cleanup: () => rmSync(dir, { recursive: true, force: true }) };
}

function makeHost(cwd) {
  const seen = { contexts: new Map(), route: null, command: null, handlers: new Map() };
  const all = {
    connection: { fetch: { register: (r) => (seen.route = r) } },
    webServer: {},
    sessions: { flush: async () => {} },
    on(ev, fn) {
      if (!seen.handlers.has(ev)) seen.handlers.set(ev, []);
      seen.handlers.get(ev).push(fn);
    },
  };
  seen.ctx = {
    tools: { register() {} },
    commands: { register: (c) => (seen.command = c) },
    systemPrompt: { context: (c) => seen.contexts.set(c.name, c.text) },
    inject(deps, cb) {
      if (deps.every((d) => Object.hasOwn(all, d))) {
        const injected = Object.fromEntries(deps.map((d) => [d, all[d]]));
        if (deps.includes("sessions")) injected.on = all.on;
        cb(injected);
      }
    },
    on: all.on,
  };
  seen.assembly = (sid, prompt) => {
    const log = [
      { type: "user/message", data: { role: "user", source: { kind: "user" }, content: [{ type: "text", text: prompt }] } },
    ];
    return {
      agent: {
        sessionId: sid,
        session: { header: { cwd }, get seq() { return log.length; }, eventAt: (i) => log[i] },
      },
    };
  };
  seen.flush = async (ev, ...args) => {
    for (const fn of seen.handlers.get(ev) || []) {
      await fn(...args);
    }
  };
  seen.postExecute = async (exec, result) => {
    let out = { kind: "accept" };
    for (const fn of seen.handlers.get("tools/post-execute") || []) {
      out = await fn(exec, result, async () => out);
    }
    return out;
  };
  return seen;
}

async function withDSHEnv(stub, tag, fn) {
  const prevBin = process.env.DEJA_BIN;
  const prevHome = process.env.DSH_HOME;
  process.env.DEJA_BIN = stub.bin;
  process.env.DSH_HOME = stub.dir;
  try {
    const { default: apply } = await import(`../index.js?${tag}`);
    const h = makeHost(stub.dir);
    apply(h.ctx, {});
    await fn(h);
  } finally {
    if (prevBin === undefined) delete process.env.DEJA_BIN;
    else process.env.DEJA_BIN = prevBin;
    if (prevHome === undefined) delete process.env.DSH_HOME;
    else process.env.DSH_HOME = prevHome;
  }
}

test("routes dsh contexts, command, compaction, tools, and session end through hook-ext", { skip }, async () => {
  const stub = makeExtStub("ok");
  try {
    await withDSHEnv(stub, "ext-ok", async (h) => {
      assert.equal(h.contexts.get("deja:project")(h.assembly("session-s1", "why")), "EXT-CTX");
      assert.equal(h.contexts.get("deja:recall")(h.assembly("session-s1", "why")), "EXT-PROMPT");

      const notesRes = await h.route.fetch(new Request("http://127.0.0.1/api/deja-notes?session=session-s1"));
      assert.deepEqual((await notesRes.json()).notes, ["EXT-NOTE-CTX", "EXT-NOTE-PROMPT"]);

      const slashRes = await h.command.handler({ rawInput: "staging queue", agent: { sessionId: "session-s1" } });
      assert.equal(slashRes.kind, "success");
      assert.match(slashRes.text, /EXT-SLASH:staging queue/);

      const readRes = await h.postExecute({ name: "read", arguments: { file_path: "a.go" }, agent: { sessionId: "session-s1", session: { header: { cwd: stub.dir } } } }, {});
      assert.equal(readRes.additionalContexts.length, 1);
      assert.equal(readRes.additionalContexts[0].content[0].text, "EXT-TOOL-BEFORE");

      const bashFail = await h.postExecute(
        { name: "bash", arguments: { command: "go test ./..." }, agent: { sessionId: "session-s1", session: { header: { cwd: stub.dir } } } },
        { isError: false, content: [{ type: "text", text: "FAIL pkg\n[exit code: 1]" }] },
      );
      assert.equal(bashFail.additionalContexts.length, 1);
      assert.equal(bashFail.additionalContexts[0].content[0].text, "EXT-TOOL-AFTER");

      const bashOk = await h.postExecute(
        { name: "bash", arguments: { command: "true" }, agent: { sessionId: "session-s1", session: { header: { cwd: stub.dir } } } },
        { isError: false, content: [{ type: "text", text: "ok\n[exit code: 0]" }] },
      );
      assert.equal(bashOk.additionalContexts, undefined);

      await h.flush("session/event", { id: "session-s1", header: { cwd: stub.dir } }, { type: "compaction/start" });
      await h.flush("session/created", { id: "session-s1", header: { cwd: stub.dir } });
      await h.flush("session/disposed", { id: "session-s1", header: { cwd: stub.dir } });
    });

    for (let i = 0; i < 40 && !stub.logged().some((e) => e.args.includes("session-end")); i++) {
      await new Promise((r) => setTimeout(r, 25));
    }
    const entries = stub.logged();
    assert.ok(entries.length >= 8, JSON.stringify(entries));
    assert.ok(entries.every((e) => e.args[0] === "hook-ext"), JSON.stringify(entries));
    const afterCall = entries.find((e) => e.args.includes("tool-after"));
    assert.ok(afterCall, "expected tool-after call for bash [exit code: 1]");
    const afterReq = JSON.parse(afterCall.input);
    assert.equal(afterReq.is_error, true);
    assert.equal(afterReq.tool_response, "FAIL pkg");
  } finally {
    stub.cleanup();
  }
});

test("falls back to legacy subcommands when hook-ext returns exit 2 non-JSON", { skip }, async () => {
  const stub = makeExtStub("legacy");
  try {
    await withDSHEnv(stub, "ext-legacy", async (h) => {
      assert.equal(h.contexts.get("deja:project")(h.assembly("session-s2", "why")), "LEGACY-CTX");
      assert.equal(h.contexts.get("deja:recall")(h.assembly("session-s2", "why")), "LEGACY-PROMPT");
      const slashRes = await h.command.handler({ rawInput: "staging", agent: { sessionId: "session-s2" } });
      assert.equal(slashRes.kind, "success");
      assert.match(slashRes.text, /LEGACY-SLASH/);

      const readRes = await h.postExecute({ name: "read", arguments: { file_path: "b.go" }, agent: { sessionId: "session-s2", session: { header: { cwd: stub.dir } } } }, {});
      assert.equal(readRes.additionalContexts[0].content[0].text, "LEGACY-TOOL-BEFORE");

      const bashFail = await h.postExecute(
        { name: "bash", arguments: { command: "make" }, agent: { sessionId: "session-s2", session: { header: { cwd: stub.dir } } } },
        { isError: false, content: [{ type: "text", text: "boom\n[exit code: 1]" }] },
      );
      assert.equal(bashFail.additionalContexts[0].content[0].text, "LEGACY-TOOL-AFTER");

      await h.flush("session/event", { id: "session-s2", header: { cwd: stub.dir } }, { type: "compaction/start" });
      await h.flush("session/created", { id: "session-s2", header: { cwd: stub.dir } });
      await h.flush("session/disposed", { id: "session-s2", header: { cwd: stub.dir } });
    });

    for (let i = 0; i < 40 && !stub.logged().some((e) => e.args[0] === "hook-session-end"); i++) {
      await new Promise((r) => setTimeout(r, 25));
    }
    const entries = stub.logged();
    assert.equal(entries.filter((e) => e.args[0] === "hook-ext").length, 1, "should cache hookExt=false after non-JSON exit 2");
    assert.ok(entries.some((e) => e.args[0] === "hook-context"));
    assert.ok(entries.some((e) => e.args[0] === "hook-prompt"));
    assert.ok(entries.some((e) => e.args[0] === "search"));
    assert.ok(entries.some((e) => e.args[0] === "hook-tool"));
    assert.ok(entries.some((e) => e.args[0] === "hook-tool-after"));
    assert.ok(entries.some((e) => e.args[0] === "hook-precompact"));
    assert.ok(entries.some((e) => e.args[0] === "hook-session-end"));
  } finally {
    stub.cleanup();
  }
});

test("does not fall back when hook-ext returns ok:false JSON envelope on exit 1", { skip }, async () => {
  const stub = makeExtStub("deny");
  try {
    await withDSHEnv(stub, "ext-deny", async (h) => {
      assert.equal(h.contexts.get("deja:project")(h.assembly("session-s3", "why")), "");
      assert.equal(h.contexts.get("deja:recall")(h.assembly("session-s3", "why")), "");
      const slashRes = await h.command.handler({ rawInput: "staging", agent: { sessionId: "session-s3" } });
      assert.equal(slashRes.kind, "error");
      assert.equal(slashRes.text, "blocked by policy");

      const readRes = await h.postExecute({ name: "read", arguments: { file_path: "c.go" }, agent: { sessionId: "session-s3", session: { header: { cwd: stub.dir } } } }, {});
      assert.equal(readRes.additionalContexts, undefined);

      const bashFail = await h.postExecute(
        { name: "bash", arguments: { command: "make" }, agent: { sessionId: "session-s3", session: { header: { cwd: stub.dir } } } },
        { isError: false, content: [{ type: "text", text: "boom\n[exit code: 1]" }] },
      );
      assert.equal(bashFail.additionalContexts, undefined);

      await h.flush("session/event", { id: "session-s3", header: { cwd: stub.dir } }, { type: "compaction/start" });
      await h.flush("session/created", { id: "session-s3", header: { cwd: stub.dir } });
      await h.flush("session/disposed", { id: "session-s3", header: { cwd: stub.dir } });
    });

    for (let i = 0; i < 40 && !stub.logged().some((e) => e.args.includes("session-end")); i++) {
      await new Promise((r) => setTimeout(r, 25));
    }
    const entries = stub.logged();
    assert.ok(entries.length >= 6, JSON.stringify(entries));
    assert.ok(entries.every((e) => e.args[0] === "hook-ext"), "expected zero legacy fallbacks on ok:false");
  } finally {
    stub.cleanup();
  }
});
