// dsh-deja — the history you already have, inside DeepSeek Harness.
//
// dsh answers questions about its own sessions through the built-in
// session-query subsystem. This plugin answers the other question: what you did
// in Claude Code, Codex, Cursor, opencode and sixteen more agents, on this
// machine, before dsh existed. The index is deja's; this file is the seam.

import { createRequire } from "node:module";
import { execFileSync } from "node:child_process";

const require = createRequire(import.meta.url);

// The tool registry lives in the host, not here. A plugin that throws on a
// missing peer takes the whole profile down with it, so both imports degrade:
// without them the command and automatic recall still work, only the
// model-facing tools are skipped.
// The tool registry lives in the host. A plugin that throws on a missing peer
// takes the whole profile down with it, so this degrades: without dsh-tools the
// command and automatic recall still work, only the model-facing tools are
// skipped.
let defineTool = null;
try {
  ({ defineTool } = await import("@deepseek-ai/dsh-tools"));
} catch {}

const PLATFORM = process.platform === "win32" ? "windows" : process.platform;
const ARCH = process.arch === "x64" ? "amd64" : process.arch;

// resolveDeja finds the binary: an explicit override first, then the platform
// package npm installed next to this one, then whatever is on PATH.
function resolveDeja() {
  if (process.env.DEJA_BIN) return process.env.DEJA_BIN;
  const exe = PLATFORM === "windows" ? "deja.exe" : "deja";
  try {
    return require.resolve(`@vshulcz/deja-vu-${PLATFORM}-${ARCH}/bin/${exe}`);
  } catch {
    return exe;
  }
}

const DEJA = resolveDeja();

// run returns stdout, or "" when deja is missing or says nothing. Memory is
// optional everywhere: a throw here would end the turn.
function run(args, input) {
  try {
    return execFileSync(DEJA, args, {
      encoding: "utf8",
      timeout: 20000,
      maxBuffer: 8 * 1024 * 1024,
      input,
      stdio: [input === undefined ? "ignore" : "pipe", "pipe", "ignore"],
    }).trim();
  } catch {
    return "";
  }
}

const NOTHING = "Nothing in this machine's history matches that.";

function tools(ctx) {
  if (!defineTool) return;

  // Every tool here answers with the text deja printed, so one output
  // declaration serves all three. The schema is plain JSON Schema — a
  // schemastery instance is rejected as "schema must be a value schema object",
  // because the host validates that this is an ordinary JSON record.
  const TEXT_OUTPUT = {
    schema: { type: "string" },
    render: (_args, value) => [{ type: "text", text: String(value) }],
  };

  ctx.tools.register(defineTool({
    name: "deja_recall",
    description:
      "Search this machine's own past AI coding sessions — every agent used on it, including months before deja was installed. Use before debugging an error or re-implementing anything that may already exist. Match on the most specific token available: an exact error string, function name, file path or flag.",
    parameters: {
      query: {
        type: "string",
        required: true,
        description: "Specific tokens to match. Several words are ANDed.",
      },
      limit: {
        type: "number",
        description: "How many sessions to return. Default 5.",
      },
    },
    output: TEXT_OUTPUT,
    execute(args) {
      const limit = Number.isFinite(args.limit) ? String(Math.max(1, Math.trunc(args.limit))) : "5";
      return Promise.resolve(run(["search", "--json", "--limit", limit, String(args.query)]) || NOTHING);
    },
  }));

  ctx.tools.register(defineTool({
    name: "deja_session",
    description:
      "A full digest of the single best-matching past session — what was tried, what was decided, what it cost. Use after deja_recall when the reasoning behind an earlier decision matters, not just that it happened.",
    parameters: {
      query: {
        type: "string",
        required: true,
        description: "A query, or a session id prefix returned by deja_recall.",
      },
    },
    output: TEXT_OUTPUT,
    execute(args) {
      return Promise.resolve(run(["ctx", String(args.query)]) || NOTHING);
    },
  }));

  ctx.tools.register(defineTool({
    name: "deja_blame",
    description:
      "The past sessions that discussed a file, so you know why it is shaped the way it is before editing, refactoring or deleting it. Session history, not git authorship.",
    parameters: {
      path: {
        type: "string",
        required: true,
        description: "Path to the file, absolute or relative to the workspace.",
      },
    },
    output: TEXT_OUTPUT,
    execute(args) {
      return Promise.resolve(run(["blame", String(args.path), "--json"]) || NOTHING);
    },
  }));
}

function command(ctx) {
  ctx.commands.register({
    name: "deja",
    description: "Search this machine's past AI coding sessions",
    input: { hint: "what to look for" },
    async handler(invocation) {
      const query = String((invocation && invocation.rawInput) || "").trim();
      if (!query) {
        return { kind: "error", text: "Say what to look for: /deja <error, file, or decision>" };
      }
      const out = run([query]);
      return { kind: "success", text: out || NOTHING };
    },
  });
}

function userText(message) {
  const content = message && message.content;
  if (!Array.isArray(content)) return typeof content === "string" ? content.trim() : "";
  return content
    .filter((part) => part && part.type === "text" && typeof part.text === "string")
    .map((part) => part.text)
    .join("\n")
    .trim();
}

// autoRecall puts the answer in front of the model without anyone asking for
// it, through the seam the host evaluates on every assembly. The obvious
// alternative — splicing a message into the "agent/pre-step" waterfall — looks
// like it works and does not: a later listener rebuilds its answer from the
// payload, and the added message is dropped with nothing reported.
function autoRecall(ctx) {
  let asked = "";
  let recalled = "";

  ctx.systemPrompt.context({
    name: "deja:recall",
    order: 120,
    text: (assembly) => {
      const agent = assembly && assembly.agent;
      if (!agent) return "";
      const prompt = lastHumanText(agent);
      if (!prompt) return "";
      if (prompt !== asked) {
        asked = prompt;
        recalled = run(["hook-prompt", "--plain"], JSON.stringify({ prompt, cwd: process.cwd() }));
      }
      // Silence is the common case: this speaks only when the history answers.
      return recalled;
    },
  });
}

// lastHumanText is the newest thing the person actually typed. At assembly
// time the message has already left the inbox and has not been appended as a
// "user/message" yet — the only durable record of it is the inbox splice that
// carried it in, so both are read. Anything a plugin contributed is skipped:
// those carry a source of their own.
function lastHumanText(agent) {
  const events = agent && agent.session && agent.session.events;
  if (!Array.isArray(events)) return "";
  for (let i = events.length - 1; i >= 0; i--) {
    const event = events[i];
    if (!event) continue;
    if (event.type === "user/message" && isHuman(event.data)) return userText(event.data);
    if (event.type === "agent/inbox/spliced") {
      const inserted = event.data && event.data.inserted;
      if (!Array.isArray(inserted)) continue;
      for (let j = inserted.length - 1; j >= 0; j--) {
        if (isHuman(inserted[j])) return userText(inserted[j]);
      }
    }
  }
  return "";
}

function isHuman(message) {
  return Boolean(message) && (!message.source || message.source.kind === "user");
}

function apply(ctx, config) {
  tools(ctx);
  command(ctx);
  if (!config || config.autoRecall !== false) autoRecall(ctx);
}

apply.inject = ["tools", "commands", "systemPrompt"];

export default apply;
