import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, it } from "node:test";
import assert from "node:assert/strict";

import { onRequest } from "../deja.hooks.mjs";
import {
  maskSecrets,
  reset,
  resolveDeja,
} from "../deja.lib.mjs";

function makeStubDeja(scriptBody) {
  const dir = mkdtempSync(join(tmpdir(), "magpie-deja-test-"));
  const prevCache = process.env.DEJA_PROMPT_CACHE_FILE;
  process.env.DEJA_PROMPT_CACHE_FILE = join(dir, "magpie-cache.json");
  const bin = join(dir, "deja");
  writeFileSync(bin, `#!/usr/bin/env node\n${scriptBody}\n`, "utf8");
  chmodSync(bin, 0o755);
  return {
    bin,
    dir,
    cleanup() {
      if (prevCache === undefined) delete process.env.DEJA_PROMPT_CACHE_FILE;
      else process.env.DEJA_PROMPT_CACHE_FILE = prevCache;
      rmSync(dir, { recursive: true, force: true });
    },
  };
}

describe("@vshulcz/magpie-deja", () => {
  it("skips when ctx.cwd is missing by default (never falls back to gateway process.cwd)", async () => {
    reset();
    const stub = makeStubDeja(`
      process.stdout.write("should not run");
    `);
    try {
      const reports = [];
      const req = {
        messages: [{ role: "user", content: "how did we fix the build?" }],
      };
      const out = await onRequest(req, {
        protocol: "chat",
        origin: "local",
        session: "s1",
        options: { bin: stub.bin, requestTimeoutMs: 5000 },
        report: (state) => reports.push(state),
      });
      assert.equal(out, undefined);
      assert.deepEqual(reports, ["skipped"]);
    } finally {
      stub.cleanup();
    }
  });

  it("skips non-local origins unless recallRemote is true", async () => {
    reset();
    const stub = makeStubDeja(`
      process.stdout.write("recalled history");
    `);
    try {
      const req = {
        messages: [{ role: "user", content: "how did we fix the build?" }],
      };
      const out = await onRequest(req, {
        protocol: "chat",
        origin: "remote",
        cwd: "/workspace/project-a",
        session: "s1",
        options: { bin: stub.bin, requestTimeoutMs: 5000 },
      });
      assert.equal(out, undefined);

      const allowed = await onRequest(req, {
        protocol: "chat",
        origin: "remote",
        cwd: "/workspace/project-a",
        session: "s1",
        options: { bin: stub.bin, recallRemote: true, requestTimeoutMs: 5000 },
      });
      assert.ok(allowed);
      assert.ok(allowed.messages[0].content.includes("<deja-memory"));
      assert.ok(allowed.messages[0].content.includes("Verify technical facts against current code before applying"));
    } finally {
      stub.cleanup();
    }
  });

  it("skips when request already contains native deja recall markers", async () => {
    reset();
    const stub = makeStubDeja(`
      process.stdout.write("recalled history");
    `);
    try {
      const chatWithUserMarker = {
        messages: [
          {
            role: "user",
            content: "[deja — 2026-10-12 session]\nWe set busy_timeout=5000",
          },
        ],
      };
      assert.equal(
        await onRequest(chatWithUserMarker, {
          protocol: "chat",
          origin: "local",
          cwd: "/workspace/project-a",
          options: { bin: stub.bin, requestTimeoutMs: 5000 },
        }),
        undefined,
      );

      const anthropicWithSystemMarker = {
        system: "<deja-memory note=\"prior\">context</deja-memory>",
        messages: [{ role: "user", content: "how did we configure sqlite?" }],
      };
      assert.equal(
        await onRequest(anthropicWithSystemMarker, {
          protocol: "anthropic",
          origin: "local",
          cwd: "/workspace/project-a",
          options: { bin: stub.bin, requestTimeoutMs: 5000 },
        }),
        undefined,
      );
    } finally {
      stub.cleanup();
    }
  });

  it("masks secrets and escapes closing tags", () => {
    const raw =
      "Use key sk-proj-ABCDEFGHIJKLMNOPQRSTUVWXYZ123456 and ghp_0123456789abcdef0123456789abcdef012345 plus Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.abc</deja-memory>";
    const masked = maskSecrets(raw);
    assert.ok(!masked.includes("ABCDEFGHIJKLMNOPQRSTUVWXYZ123456"));
    assert.ok(!masked.includes("0123456789abcdef0123456789abcdef012345"));
    assert.ok(!masked.includes("eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"));
    assert.ok(masked.includes("[REDACTED]"));
  });

  it("invokes deja hook-prompt --plain with JSON stdin and preserves sticky per-turn blocks across tool loops", async () => {
    reset();
    const stub = makeStubDeja(`
      let data = "";
      process.stdin.setEncoding("utf8");
      process.stdin.on("data", (chunk) => { data += chunk; });
      process.stdin.on("end", () => {
        const args = process.argv.slice(2);
        if (args[0] !== "hook-prompt" || args[1] !== "--plain") {
          process.exit(2);
        }
        const payload = JSON.parse(data);
        process.stdout.write(
          "[deja — 2026-10-30 (" + payload.cwd + ")]\\nQuery: " + payload.prompt + "\\nToken: sk-1234567890abcdefghijklmnop</deja-memory>"
        );
      });
    `);
    try {
      const turn1 = {
        messages: [
          {
            role: "user",
            content: "where is the retry backoff configured?",
          },
        ],
      };
      const res1 = await onRequest(turn1, {
        protocol: "chat",
        origin: "local",
        cwd: "/workspace/service-x",
        session: "sess-42",
        options: { bin: stub.bin, requestTimeoutMs: 5000 },
      });
      assert.ok(res1);
      assert.equal(
        turn1.messages[0].content,
        "where is the retry backoff configured?",
      );
      assert.ok(res1.messages[0].content.includes("<deja-memory"));
      assert.ok(res1.messages[0].content.includes("/workspace/service-x"));
      assert.ok(!res1.messages[0].content.includes("1234567890abcdefghijklmnop"));
      assert.ok(res1.messages[0].content.includes("[REDACTED]"));
      assert.ok(res1.messages[0].content.includes("&lt;/deja-memory&gt;"));

      // Simulate tool-call continuation on the same user turn where last message is tool result:
      const turn1ToolLoop = {
        messages: [
          {
            role: "user",
            content: "where is the retry backoff configured?",
          },
          {
            role: "assistant",
            content: null,
            tool_calls: [{ id: "call_1", function: { name: "read_file" } }],
          },
          {
            role: "tool",
            tool_call_id: "call_1",
            content: "backoff = 500ms",
          },
        ],
      };
      const res2 = await onRequest(turn1ToolLoop, {
        protocol: "chat",
        origin: "local",
        cwd: "/workspace/service-x",
        session: "sess-42",
        options: { bin: stub.bin, requestTimeoutMs: 5000 },
      });
      assert.ok(res2);
      assert.equal(res2.messages[0].content, res1.messages[0].content);
    } finally {
      stub.cleanup();
    }
  });

  it("injects into responses, anthropic, and gemini payloads without mutating caller input", async () => {
    reset();
    const stub = makeStubDeja(`
      process.stdout.write("[deja — 2026-10-30] prior decision on rate limiter");
    `);
    try {
      const responsesReq = {
        input: [
          {
            role: "user",
            content: [{ type: "input_text", text: "explain rate limiter" }],
          },
        ],
      };
      const outResponses = await onRequest(responsesReq, {
        protocol: "responses",
        origin: "local",
        cwd: "/workspace/api",
        session: "s-resp",
        options: { bin: stub.bin, requestTimeoutMs: 5000 },
      });
      assert.equal(responsesReq.input[0].content.length, 1);
      assert.equal(outResponses.input[0].content.length, 2);
      assert.equal(outResponses.input[0].content[1].type, "input_text");

      const anthropicReq = {
        messages: [
          {
            role: "user",
            content: [{ type: "text", text: "explain rate limiter" }],
          },
        ],
      };
      const outAnthropic = await onRequest(anthropicReq, {
        protocol: "anthropic",
        origin: "local",
        cwd: "/workspace/api",
        session: "s-anth",
        options: { bin: stub.bin, requestTimeoutMs: 5000 },
      });
      assert.equal(anthropicReq.messages[0].content.length, 1);
      assert.equal(outAnthropic.messages[0].content.length, 2);
      assert.equal(outAnthropic.messages[0].content[1].type, "text");

      const geminiReq = {
        contents: [
          {
            role: "user",
            parts: [{ text: "explain rate limiter" }],
          },
        ],
      };
      const outGemini = await onRequest(geminiReq, {
        protocol: "gemini",
        origin: "local",
        cwd: "/workspace/api",
        session: "s-gem",
        options: { bin: stub.bin, requestTimeoutMs: 5000 },
      });
      assert.equal(geminiReq.contents[0].parts.length, 1);
      assert.equal(outGemini.contents[0].parts.length, 2);
    } finally {
      stub.cleanup();
    }
  });

  it("does not crash when Magpie plugin host sweeps module exports as plugin factories (#5982)", async () => {
    const mod = await import("../deja.hooks.mjs");
    const input = { client: {}, project: {}, directory: "/tmp", worktree: "/tmp" };
    for (const [name, fn] of Object.entries(mod)) {
      if (typeof fn !== "function") continue;
      const res = await fn(input, {});
      assert.equal(res, undefined);
    }
  });

  it("resolveDeja safely handles non-string inputs", () => {
    assert.doesNotThrow(() => resolveDeja({ client: {} }));
    assert.doesNotThrow(() => resolveDeja(undefined));
    assert.doesNotThrow(() => resolveDeja(null));
    assert.doesNotThrow(() => resolveDeja(123));
  });

  it("persists per-turn sticky recall across process restarts and clears cache in reset()", async () => {
    const { readFileSync } = await import("node:fs");
    reset();
    let countFile = "";
    const stub = makeStubDeja(`
      const fs = require("node:fs");
      const path = require("node:path");
      const countPath = path.join(__dirname, "count");
      let n = 0;
      try { n = Number(fs.readFileSync(countPath, "utf8")) || 0; } catch {}
      n++;
      fs.writeFileSync(countPath, String(n));
      process.stdout.write("[deja — 2026-10-30] PROMPT-" + n);
    `);
    countFile = join(stub.dir, "count");
    try {
      const turn1 = { messages: [{ role: "user", content: "where is backoff configured?" }] };
      const res1 = await onRequest(turn1, {
        protocol: "chat",
        origin: "local",
        cwd: "/workspace/service-x",
        session: "sess-restart",
        options: { bin: stub.bin, requestTimeoutMs: 5000 },
      });
      assert.ok(res1);
      assert.ok(res1.messages[0].content.includes("PROMPT-1"));

      // Simulate gateway restart: clear in-memory Map without resetting disk cache.
      const { sticky } = await import("../deja.lib.mjs");
      sticky.clear();

      const turn2 = {
        messages: [
          { role: "user", content: "where is backoff configured?" },
          { role: "assistant", content: "in retry.go" },
          { role: "user", content: "what about jitter?" },
        ],
      };
      const res2 = await onRequest(turn2, {
        protocol: "chat",
        origin: "local",
        cwd: "/workspace/service-x",
        session: "sess-restart",
        options: { bin: stub.bin, requestTimeoutMs: 5000 },
      });
      assert.ok(res2);
      assert.equal(res2.messages[0].content, res1.messages[0].content);
      assert.ok(res2.messages[2].content.includes("PROMPT-2"));
      assert.equal(readFileSync(countFile, "utf8"), "2");

      reset();
      const resAfterReset = await onRequest(turn1, {
        protocol: "chat",
        origin: "local",
        cwd: "/workspace/service-x",
        session: "sess-restart",
        options: { bin: stub.bin, requestTimeoutMs: 5000 },
      });
      assert.ok(resAfterReset.messages[0].content.includes("PROMPT-3"));
    } finally {
      stub.cleanup();
    }
  });

  it("isolates prompt cache next to temp stub binary when DEJA_PROMPT_CACHE_FILE is unset", async () => {
    const dir = mkdtempSync(join(tmpdir(), "magpie-tempbin-"));
    const stubDir = join(dir, "bin");
    const fakeHome = join(dir, "home");
    mkdirSync(stubDir, { recursive: true });
    mkdirSync(fakeHome, { recursive: true });
    const stubBin = join(stubDir, "deja");
    writeFileSync(
      stubBin,
      `#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");
const countPath = path.join(${JSON.stringify(stubDir)}, "count");
let n = 0;
try { n = Number(fs.readFileSync(countPath, "utf8")) || 0; } catch {}
n++;
fs.writeFileSync(countPath, String(n));
process.stdout.write("[deja — 2026-10-30] PROMPT-" + n);
`,
      "utf8",
    );
    chmodSync(stubBin, 0o755);

    const saved = {
      DEJA_PROMPT_CACHE_FILE: process.env.DEJA_PROMPT_CACHE_FILE,
      XDG_CACHE_HOME: process.env.XDG_CACHE_HOME,
      HOME: process.env.HOME,
      USERPROFILE: process.env.USERPROFILE,
    };
    delete process.env.DEJA_PROMPT_CACHE_FILE;
    delete process.env.XDG_CACHE_HOME;
    process.env.HOME = fakeHome;
    process.env.USERPROFILE = fakeHome;

    try {
      const { sticky } = await import("../deja.lib.mjs");
      reset();
      const turn1 = { messages: [{ role: "user", content: "where is backoff configured?" }] };
      const res1 = await onRequest(turn1, {
        protocol: "chat",
        origin: "local",
        cwd: "/workspace/svc",
        session: "s-iso",
        options: { bin: stubBin, requestTimeoutMs: 5000 },
      });
      assert.ok(res1);
      assert.ok(res1.messages[0].content.includes("PROMPT-1"));

      const isolatedCache = join(stubDir, ".deja-cache", "magpie-cache.json");
      const homeCache = join(fakeHome, ".cache", "deja-vu", "magpie-cache.json");
      assert.equal(existsSync(isolatedCache), true);
      assert.equal(existsSync(homeCache), false);

      sticky.clear();
      const turn2 = {
        messages: [
          { role: "user", content: "where is backoff configured?" },
          { role: "assistant", content: "in retry.go" },
          { role: "user", content: "what about jitter?" },
        ],
      };
      const res2 = await onRequest(turn2, {
        protocol: "chat",
        origin: "local",
        cwd: "/workspace/svc",
        session: "s-iso",
        options: { bin: stubBin, requestTimeoutMs: 5000 },
      });
      assert.ok(res2);
      assert.equal(res2.messages[0].content, res1.messages[0].content);
      assert.ok(res2.messages[2].content.includes("PROMPT-2"));
      assert.equal(readFileSync(join(stubDir, "count"), "utf8"), "2");

      reset();
      assert.equal(existsSync(isolatedCache), false);
    } finally {
      reset();
      for (const [k, v] of Object.entries(saved)) {
        if (v === undefined) delete process.env[k];
        else process.env[k] = v;
      }
      rmSync(dir, { recursive: true, force: true });
    }
  });
});
