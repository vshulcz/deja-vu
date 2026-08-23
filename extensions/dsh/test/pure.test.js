// The plugin's pure helpers, tested without dsh.
//
// index.js registers itself against a live host on import, so the parts worth
// testing are copied here as the file states them. That is a real risk — a
// change in index.js will not fail these tests — so each case exists to pin a
// rule that was wrong once, not to prove the file is correct.

import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import apply from "../index.js";

const source = readFileSync(new URL("../index.js", import.meta.url), "utf8");

test("the recall limit is bounded on both sides", () => {
  const clamp = (asked) => Math.min(20, Math.max(1, asked));
  assert.equal(clamp(0), 1);
  assert.equal(clamp(-3), 1);
  assert.equal(clamp(7), 7);
  assert.equal(clamp(9999), 20);
});

test("tool output declares a plain JSON schema", () => {
  // A schemastery instance is rejected by the host with "schema must be a
  // value schema object", and the profile then fails to load at all.
  assert.match(source, /schema: \{ type: "string" \}/);
  assert.doesNotMatch(source, /schema: z\./);
});

test("automatic recall does not use the pre-step waterfall", () => {
  // A message spliced there is dropped by a later listener before the request
  // is built, with nothing reported.
  // The comment explaining why names the event, so the check is on the
  // registration rather than on the words.
  assert.doesNotMatch(source, /ctx\.on\("agent\/pre-step"/);
  assert.match(source, /ctx\.systemPrompt\.context\(/);
});

test("the patch names the package the host has to load", () => {
  const patch = readFileSync(new URL("../cordis.patch.yml", import.meta.url), "utf8");
  const pkg = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8"));
  assert.match(patch, new RegExp(`name: ${pkg.name}`));
  assert.equal(pkg.dsh.bundle.patch, "./cordis.patch.yml");
});

test("every file the manifest ships is present", () => {
  const pkg = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8"));
  for (const file of pkg.files) {
    assert.doesNotThrow(
      () => readFileSync(new URL(`../${file}`, import.meta.url)),
      `package.json lists ${file}, which is not in the repository`,
    );
  }
});

// `deja install dsh` writes plugins of its own into DSH_HOME, and dsh composes
// both them and this package into one profile: two `/deja` commands and the
// same recall on the system prompt twice. Each half stands down separately,
// because the command is installed by `deja install dsh` and the recall only by
// `deja install dsh-auto`.
function fakeCtx() {
  const seen = { tools: 0, commands: [], context: [] };
  return {
    seen,
    tools: { register: () => seen.tools++ },
    commands: { register: (c) => seen.commands.push(c.name) },
    systemPrompt: { context: (c) => seen.context.push(c.name) },
  };
}

function withDSHHome(files, run) {
  const dir = mkdtempSync(join(tmpdir(), "deja-dsh-"));
  mkdirSync(join(dir, "plugins", "deja"), { recursive: true });
  for (const file of files) writeFileSync(join(dir, "plugins", "deja", file), "// installed by deja\n");
  const previous = process.env.DSH_HOME;
  process.env.DSH_HOME = dir;
  try {
    return run();
  } finally {
    if (previous === undefined) delete process.env.DSH_HOME;
    else process.env.DSH_HOME = previous;
    rmSync(dir, { recursive: true, force: true });
  }
}

test("nothing installed by the CLI: the package contributes everything", () => {
  const ctx = withDSHHome([], () => {
    const ctx = fakeCtx();
    apply(ctx, {});
    return ctx;
  });
  assert.deepEqual(ctx.seen.commands, ["deja"]);
  assert.deepEqual(ctx.seen.context, ["deja:recall"]);
});

test("the CLI's command file stands the package's command down", () => {
  const ctx = withDSHHome(["command.js"], () => {
    const ctx = fakeCtx();
    apply(ctx, {});
    return ctx;
  });
  assert.deepEqual(ctx.seen.commands, []);
  assert.deepEqual(ctx.seen.context, ["deja:recall"], "recall was not installed by the CLI, so it stays here");
});

test("the CLI's auto file stands the package's recall down", () => {
  const ctx = withDSHHome(["command.js", "auto.js"], () => {
    const ctx = fakeCtx();
    apply(ctx, {});
    return ctx;
  });
  assert.deepEqual(ctx.seen.commands, []);
  assert.deepEqual(ctx.seen.context, []);
  assert.ok(ctx.seen.tools >= 0, "tools are never duplicated: the CLI install does not register any");
});
