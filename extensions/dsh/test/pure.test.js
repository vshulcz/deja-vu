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
import { argv, contributions, exitStatus, guarded, resultText } from "../lib.js";

const source = readFileSync(new URL("../index.js", import.meta.url), "utf8");

test("the recall limit is bounded on both sides", () => {
  const clamp = (asked) => Math.min(20, Math.max(1, asked));
  assert.equal(clamp(0), 1);
  assert.equal(clamp(-3), 1);
  assert.equal(clamp(7), 7);
  assert.equal(clamp(9999), 20);
});

test("a query that is a command name is still searched", () => {
  // deja's bare-query path dispatches the first word, so `/deja version`
  // printed a version number instead of searching for the word.
  assert.deepEqual(argv("search", [], "version"), ["search", "version"]);
  assert.deepEqual(argv("search", ["--json"], "index"), ["search", "--json", "index"]);
});

test("a query that starts with a dash is not read as a flag", () => {
  // Without the terminator deja exits on an unknown flag, which the caller
  // cannot tell from an empty history.
  assert.deepEqual(argv("search", [], "--no-verify"), ["search", "--", "--no-verify"]);
  assert.deepEqual(argv("fix", [], "-race detected"), ["fix", "--", "-race detected"]);
  // Flags the plugin sends itself stay ahead of the terminator, or deja reads
  // them as part of the query.
  assert.deepEqual(argv("search", ["--json", "--limit", "5"], "--all-matches"), [
    "search",
    "--json",
    "--limit",
    "5",
    "--",
    "--all-matches",
  ]);
});

test("the terminator is sent only when the query needs it", () => {
  // A deja too old to know `--` on this subcommand would otherwise fail on
  // every ordinary query, not just the ones that start with a dash.
  assert.deepEqual(argv("how", [], "run the tests"), ["how", "run the tests"]);
  assert.deepEqual(argv("ctx", [], "the checkout worker"), ["ctx", "the checkout worker"]);
});

test("every query the plugin sends goes through argv", () => {
  // The rules above are worth nothing if a call site passes its text straight
  // through, which is how both bugs got in.
  // A literal list is deja's own vocabulary — `run(["version"])` and the hook.
  // Anything else in that position is somebody's text, and it belongs in
  // argv(), which names the command and ends the flags.
  const calls = source.match(/run\(\[[^\]]*\]/g) || [];
  for (const call of calls) {
    assert.match(call, /^run\(\["/, `${call} builds a call out of something other than deja's own words`);
    assert.doesNotMatch(call, /String\(args\./, `${call} passes a query to deja without argv()`);
  }
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
  // The one pre-step listener only waits for a compaction capture and hands
  // the decision back untouched.
  const preStep = source.split('ctx.on("agent/pre-step"').slice(1);
  assert.ok(preStep.length <= 1);
  for (const body of preStep) {
    const handler = body.slice(0, body.indexOf("});"));
    assert.doesNotMatch(handler, /messages/);
    assert.match(handler, /return decision;/);
  }
  assert.match(source, /ctx\.systemPrompt\.context\(/);
});

test("a compaction is captured and its packet asked for by session", () => {
  // compaction/start leaves the shadowed turns in the log; deja reads them by
  // the session's uuid, and only a prompt that names the session gets the
  // packet back.
  assert.match(source, /event\.type !== "compaction\/start"/);
  assert.match(source, /\["hook-precompact"\]/);
  assert.match(source, /harness: "deepseek"/);
  assert.match(source, /replace\(\/\^session-\/, ""\)/);
  assert.match(source, /JSON\.stringify\(\{ prompt, cwd, session_id: sid \}\)/);
});

test("the project digest opens the session once and asks deja for it", () => {
  // agent/session-start is emit-only, so the digest rides the same assembly
  // seam the recall does; deja_once is what keeps it to one turn per session.
  assert.match(source, /name: "deja:project"/);
  assert.match(source, /"hook-context", "--plain"/);
  assert.match(source, /deja_once: true/);
});

test("recall and the digest ask about the session's workspace", () => {
  // One dsh web process serves sessions from every workspace and never changes
  // directory, so process.cwd() is only where dsh was launched. The session
  // header carries the directory the session was opened in.
  assert.match(source, /agent\.session && agent\.session\.header/);
  assert.match(source, /header && header\.cwd/);
  assert.doesNotMatch(source, /cwd: process\.cwd\(\)/);
  // The same question asked in a second workspace is asked again, not answered
  // from the first workspace's cache.
  assert.match(source, /prompt !== asked \|\| cwd !== askedIn \|\| sid !== askedBy/);
});

// dsh refuses a name one of its registries already holds — "prompt context
// deja:recall is already registered", "command deja is already registered" —
// and the failure is not local: the whole profile fails to load, so a second
// copy of this plugin costs the user their agent.
test("a refused registration is reported, not thrown", () => {
  assert.equal(guarded(() => {}), true);
  assert.equal(
    guarded(() => {
      throw new Error('command "deja" is already registered');
    }),
    false,
  );
});

test("every registration the plugin makes goes through the guard", () => {
  // The tools cannot be reached from the case below — registering them needs
  // the dsh-tools peer the host provides — so the rule is pinned here.
  // Whitespace-free, so a call broken across lines reads the same as one that
  // is not.
  const compact = source.replace(/\s+/g, "");
  const calls = /ctx\.(?:tools\.register|commands\.register|systemPrompt\.context)\(/g;
  let total = 0;
  for (const m of compact.matchAll(calls)) {
    total++;
    assert.ok(
      compact.slice(0, m.index).endsWith("guarded(()=>"),
      `${m[0]} is not wrapped in guarded()`,
    );
  }
  assert.equal(total, 9, "six tools, the command, the project digest and the recall");
});

test("a name the host already holds does not take the profile down", () => {
  const taken = new Set();
  const refusing = {
    tools: { register: () => { throw new Error("tool is already registered"); } },
    commands: {
      register: (c) => {
        if (taken.has(c.name)) throw new Error(`command "${c.name}" is already registered`);
        taken.add(c.name);
      },
    },
    systemPrompt: {
      context: (c) => {
        if (taken.has(c.name)) throw new Error(`prompt context "${c.name}" is already registered`);
        taken.add(c.name);
      },
    },
  };
  withDSHHome([], () => {
    assert.doesNotThrow(() => apply(refusing, {}), "first load");
    assert.doesNotThrow(() => apply(refusing, {}), "second load, every name taken");
  });
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
  // The tool count is not asserted: registering them needs the dsh-tools peer,
  // which the host provides and this test does not have.
  assert.deepEqual(ctx.seen.commands, ["deja"]);
  assert.deepEqual(ctx.seen.context, ["deja:project", "deja:recall"]);
});

test("the CLI's install stands the command and the tools down", () => {
  // command.js and the mcp-deja profile row are written by the same install, so
  // its presence means the six answers are already reachable over MCP.
  const ctx = withDSHHome(["command.js"], () => {
    const ctx = fakeCtx();
    apply(ctx, {});
    return ctx;
  });
  assert.equal(ctx.seen.tools, 0);
  assert.deepEqual(ctx.seen.commands, []);
  assert.deepEqual(ctx.seen.context, ["deja:project", "deja:recall"], "recall was not installed by the CLI, so it stays here");
});

test("the CLI's auto file stands the package's recall down", () => {
  const ctx = withDSHHome(["command.js", "auto.js"], () => {
    const ctx = fakeCtx();
    apply(ctx, {});
    return ctx;
  });
  assert.equal(ctx.seen.tools, 0);
  assert.deepEqual(ctx.seen.commands, []);
  assert.deepEqual(ctx.seen.context, []);
});

// The registration decision on its own: the cases above cannot see the tools,
// because registering them needs the dsh-tools peer the host provides.
test("contributions fills the gaps and never repeats the installer", () => {
  assert.deepEqual(contributions({}, {}), { tools: true, command: true, recall: true });
  assert.deepEqual(contributions({ command: true }, {}), { tools: false, command: false, recall: true });
  assert.deepEqual(contributions({ command: true, auto: true }, {}), { tools: false, command: false, recall: false });
  assert.deepEqual(contributions({ auto: true }, {}), { tools: true, command: true, recall: false });
  assert.deepEqual(contributions({}, { autoRecall: false }), { tools: true, command: true, recall: false });
  assert.deepEqual(contributions(undefined, undefined), { tools: true, command: true, recall: true });
});

// The point of action `deja install dsh-auto` wires: a read, edit or write gets
// the file's history and a failed command its fix, both on tools/post-execute.
// The package lagged here, so a profile with only the package got neither.
test("the package answers a tool result the way the installer does", () => {
  assert.match(source, /ctx\.on\("tools\/post-execute"/);
  assert.match(source, /\["hook-tool", "--plain"\]/);
  assert.match(source, /\["hook-tool-after", "--plain"\]/);
  assert.match(source, /\["hook-session-end"\]/);
  assert.match(source, /session_id: sessionId\(exec\.agent\)/);
  const ctx = withDSHHome([], () => {
    const ctx = fakeCtx();
    const events = [];
    ctx.on = (name) => events.push(name);
    apply(ctx, {});
    return { events };
  });
  for (const name of ["tools/post-execute", "session/created", "session/disposed"]) {
    assert.ok(ctx.events.includes(name), `${name} is not listened to`);
  }
});

test("a command's exit is read off its last line only", () => {
  assert.deepEqual(exitStatus("boom\n[exit code: 2]"), { body: "boom", failed: true });
  assert.deepEqual(exitStatus("ok\n[exit code: 0]"), { body: "ok", failed: false });
  assert.deepEqual(exitStatus("x\n[killed by signal: SIGKILL]"), { body: "x", failed: true });
  assert.deepEqual(exitStatus("quoted [exit code: 1] mid-output"), { body: "quoted [exit code: 1] mid-output", failed: false });
  assert.equal(resultText({ content: [{ type: "text", text: "a" }, { type: "image" }, { type: "text", text: "b" }] }), "a\nb");
});
