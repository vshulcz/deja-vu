import { chmodSync, existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { test } from "node:test"
import assert from "node:assert/strict"

import plugin from "../index.mjs"

function makeStubDeja() {
  const dir = mkdtempSync(join(tmpdir(), "deja-oc-restart-"))
  const bin = join(dir, "deja")
  const script = `#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");
const dir = ${JSON.stringify(dir)};
const args = process.argv.slice(2);
const cmd = args[0] || "";
const bump = (name) => {
  const f = path.join(dir, "cnt_" + name);
  let n = 0;
  try { n = Number(fs.readFileSync(f, "utf8")) || 0; } catch {}
  n++;
  fs.writeFileSync(f, String(n));
  return n;
};
if (cmd === "version") { process.stdout.write("0.0.0\\n"); process.exit(0); }
const raw = fs.readFileSync(0, "utf8");
let payload = {};
try { payload = JSON.parse(raw); } catch {}
if (cmd === "hook-context") {
  const n = bump("ctx");
  if (payload.deja_once && payload.session_id) {
    const marker = path.join(dir, "once_" + payload.session_id.replace(/[^a-zA-Z0-9_-]/g, "_"));
    if (fs.existsSync(marker)) process.exit(0);
    fs.writeFileSync(marker, "1");
  }
  process.stdout.write("DIGEST-" + n + "\\n");
} else if (cmd === "hook-precompact" || cmd === "hook-session-end") {
  bump(cmd);
  fs.appendFileSync(path.join(dir, "events.log"), JSON.stringify({ cmd, payload }) + "\\n");
}
`
  writeFileSync(bin, script, { encoding: "utf8" })
  chmodSync(bin, 0o755)
  const count = (name) => {
    const f = join(dir, "cnt_" + name)
    return existsSync(f) ? Number(readFileSync(f, "utf8")) || 0 : 0
  }
  const events = () => {
    const f = join(dir, "events.log")
    if (!existsSync(f)) return []
    return readFileSync(f, "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l))
  }
  return { dir, bin, count, events, cleanup: () => rmSync(dir, { recursive: true, force: true }) }
}

function registerInstance(bin) {
  const hooks = {}
  const on = {}
  plugin.register({
    pluginConfig: { tools: false, bin },
    on: (name, fn) => { on[name] = fn },
    registerHook: (name, fn) => { hooks[name] = fn },
  })
  return { hooks, on }
}

test("openclaw package persists Project Context digest and seen map across turns and restarts", async () => {
  const s = makeStubDeja()
  const prevState = process.env.OPENCLAW_STATE_DIR
  process.env.OPENCLAW_STATE_DIR = join(s.dir, "nonexistent-openclaw-state")
  try {
    const p1 = registerInstance(s.bin)

    // Turn 1: initial agent:bootstrap asks hook-context once and populates DEJA-RECALL.md.
    const ctx1 = { workspaceDir: "/workspace/proj-a", bootstrapFiles: [], sessionKey: "agent:main:s1", sessionId: "uuid-s1" }
    await p1.hooks["agent:bootstrap"]({ type: "agent", action: "bootstrap", sessionKey: "agent:main:s1", context: ctx1 })
    assert.equal(ctx1.bootstrapFiles.length, 1)
    assert.equal(ctx1.bootstrapFiles[0].name, "DEJA-RECALL.md")
    assert.equal(ctx1.bootstrapFiles[0].content, "DIGEST-1")
    assert.equal(s.count("ctx"), 1)

    // Turn 2: fresh bootstrapFiles array must receive the exact same digest without calling deja again.
    const ctx2 = { workspaceDir: "/workspace/proj-a", bootstrapFiles: [], sessionKey: "agent:main:s1", sessionId: "uuid-s1" }
    await p1.hooks["agent:bootstrap"]({ type: "agent", action: "bootstrap", sessionKey: "agent:main:s1", context: ctx2 })
    assert.equal(ctx2.bootstrapFiles.length, 1)
    assert.equal(ctx2.bootstrapFiles[0].content, "DIGEST-1")
    assert.equal(s.count("ctx"), 1)

    // Heartbeat on an unwarmed session must not consume deja_once or call hook-context.
    const ctxHbNew = { workspaceDir: "/workspace/proj-a", bootstrapFiles: [], sessionKey: "agent:hb:1", sessionId: "uuid-hb", trigger: "heartbeat" }
    await p1.hooks["agent:bootstrap"]({ type: "agent", action: "bootstrap", sessionKey: "agent:hb:1", context: ctxHbNew })
    assert.equal(ctxHbNew.bootstrapFiles.length, 0)
    assert.equal(s.count("ctx"), 1)

    // Gateway restart: fresh plugin registration must keep DIGEST-1 and seen mapping on disk.
    const p2 = registerInstance(s.bin)
    const ctx3 = { workspaceDir: "/workspace/proj-a", bootstrapFiles: [], sessionKey: "agent:main:s1", sessionId: "uuid-s1" }
    await p2.hooks["agent:bootstrap"]({ type: "agent", action: "bootstrap", sessionKey: "agent:main:s1", context: ctx3 })
    assert.equal(ctx3.bootstrapFiles.length, 1)
    assert.equal(ctx3.bootstrapFiles[0].content, "DIGEST-1")
    assert.equal(s.count("ctx"), 1)

    // Compaction and session_end carrying only sessionKey after restart still resolve transcript id and workspace.
    await p2.on.before_compaction({ sessionFile: "/workspace/proj-a/t.jsonl" }, { sessionKey: "agent:main:s1" })
    await p2.on.session_end({}, { sessionKey: "agent:main:s1" })
    assert.deepEqual(s.events(), [
      {
        cmd: "hook-precompact",
        payload: { session_id: "uuid-s1", transcript_path: "/workspace/proj-a/t.jsonl", cwd: "/workspace/proj-a", harness: "openclaw" },
      },
      {
        cmd: "hook-session-end",
        payload: { session_id: "uuid-s1", cwd: "/workspace/proj-a" },
      },
    ])
  } finally {
    if (prevState === undefined) delete process.env.OPENCLAW_STATE_DIR
    else process.env.OPENCLAW_STATE_DIR = prevState
    s.cleanup()
  }
})
