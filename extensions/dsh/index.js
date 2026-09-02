// dsh-deja — the history you already have, inside DeepSeek Harness.
//
// dsh answers questions about its own sessions through the built-in
// session-query subsystem. This plugin answers the other question: what you did
// in Claude Code, Codex, Cursor, opencode and sixteen more agents, on this
// machine, before dsh existed. The index is deja's; this file is the seam.

import { createRequire } from "node:module";
import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

import { argv, contributions, guarded } from "./lib.js";

const require = createRequire(import.meta.url);

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

// resolveDeja picks the binary in the order a user would expect: what they
// pointed at, then the deja they installed themselves and keep current with
// `deja update` or a package manager, and only then the copy npm brought along
// with this plugin. Each candidate is asked for its version rather than
// trusted, because a name on PATH that does not run is worse than no name.
function resolveDeja() {
  const exe = PLATFORM === "windows" ? "deja.exe" : "deja";
  const candidates = [process.env.DEJA_BIN, exe];
  try {
    candidates.push(require.resolve(`@vshulcz/deja-vu-${PLATFORM}-${ARCH}/bin/${exe}`));
  } catch {}

  for (const candidate of candidates) {
    if (!candidate) continue;
    try {
      execFileSync(candidate, ["version"], {
        encoding: "utf8",
        timeout: 5000,
        stdio: ["ignore", "pipe", "ignore"],
      });
      return candidate;
    } catch {}
  }
  // Nothing answered. Keep the plain name so the failure a user sees names the
  // thing that is missing.
  return exe;
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
const MISSING =
  "deja is not installed on this machine, so there is no history to search. " +
  "Install it with: curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh";

// installed answers whether the binary picked at load actually runs. Without
// this every tool would report an empty history to a user who simply never
// installed deja, which reads as "you have no past" rather than "nothing is
// here to read it".
const INSTALLED = run(["version"]) !== "";

function answer(text) {
  if (INSTALLED) return text || NOTHING;
  return MISSING;
}


function tools(ctx) {
  if (!defineTool) return;

  // Every tool here answers with the text deja printed, so one output
  // declaration serves them all. The schema is plain JSON Schema — a
  // schemastery instance is rejected as "schema must be a value schema object",
  // because the host validates that this is an ordinary JSON record.
  const TEXT_OUTPUT = {
    schema: { type: "string" },
    render: (_args, value) => [{ type: "text", text: String(value) }],
  };

  guarded(() => ctx.tools.register(defineTool({
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
      // deja itself caps the window; asking for a hundred sessions would
      // spend the model's context on a tail nobody reads.
      const asked = Number.isFinite(args.limit) ? Math.trunc(args.limit) : 5;
      const limit = String(Math.min(20, Math.max(1, asked)));
      return Promise.resolve(answer(run(argv("search", ["--json", "--limit", limit], args.query))));
    },
  })));

  guarded(() => ctx.tools.register(defineTool({
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
      return Promise.resolve(answer(run(argv("ctx", [], args.query))));
    },
  })));

  guarded(() => ctx.tools.register(defineTool({
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
      return Promise.resolve(answer(run(argv("blame", ["--json"], args.path))));
    },
  })));

  guarded(() => ctx.tools.register(defineTool({
    name: "deja_fix",
    description:
      "What this machine ran after that same error before, in the sessions where the error did not come back. Paste the failing output verbatim rather than a paraphrase — the match is on the error's own words.",
    parameters: {
      error: {
        type: "string",
        required: true,
        description: "The failing output, copied as it was printed.",
      },
    },
    output: TEXT_OUTPUT,
    execute(args) {
      return Promise.resolve(answer(run(argv("fix", [], args.error))));
    },
  })));

  guarded(() => ctx.tools.register(defineTool({
    name: "deja_how",
    description:
      "The real invocation this machine uses for a build, test, deploy or script, with the flags it actually ran, ordered by how many sessions ran it. A guessed command is plausible and fails on this setup.",
    parameters: {
      what: {
        type: "string",
        required: true,
        description: "The thing to run: a tool, a task, a script name.",
      },
    },
    output: TEXT_OUTPUT,
    execute(args) {
      return Promise.resolve(answer(run(argv("how", [], args.what))));
    },
  })));

  guarded(() => ctx.tools.register(defineTool({
    name: "deja_remember",
    description:
      "Store one durable decision once it is settled, as a single self-contained fact that will make sense months later. Not transcripts, not a summary of the conversation, and not anything already obvious from the code.",
    parameters: {
      text: {
        type: "string",
        required: true,
        description: "The decision, in one or two sentences, with the reason it was taken.",
      },
    },
    output: TEXT_OUTPUT,
    execute(args) {
      const written = run(argv("remember", [], args.text));
      if (!INSTALLED) return Promise.resolve(MISSING);
      return Promise.resolve(written || "deja did not record that.");
    },
  })));
}

function command(ctx) {
  guarded(() => ctx.commands.register({
    name: "deja",
    description: "Search this machine's past AI coding sessions",
    input: { hint: "what to look for" },
    async handler(invocation) {
      const query = String((invocation && invocation.rawInput) || "").trim();
      if (!query) {
        return { kind: "error", text: "Say what to look for: /deja <error, file, or decision>" };
      }
      // Named, not handed over as deja's first word: the bare-query path
      // dispatches a word that happens to be a command, so `/deja version`
      // printed a version number and `/deja index` rebuilt the index, and one
      // of the words people most want history about is `install`.
      const out = run(argv("search", [], query));
      return { kind: INSTALLED ? "success" : "error", text: answer(out) };
    },
  }));
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
//
// The registration is guarded because the host throws "prompt context
// deja:recall is already registered" on a second copy in the same profile, and
// that failure is not local: the whole profile fails to load, so a duplicate
// memory plugin costs the user their agent. One registration is all recall
// needs. installedByCLI() below is the first line of defence; this is the one
// that holds when the installer wrote to a different DSH_HOME than the profile
// boots from.
function autoRecall(ctx) {
  let asked = "";
  let recalled = "";

  guarded(() =>
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
    }),
  );
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

// `deja install dsh` writes plugins of its own into DSH_HOME and adds them to
// the profile, so a user who ran the installer and then added this package has
// both: two `/deja` commands, the same recall on the system prompt twice, and
// deja's MCP server answering the same six questions the tools here do. What
// the installer wrote wins — it is the copy `deja install` keeps current — and
// this package contributes only the parts that are missing.
//
// The two halves are separate on purpose: `deja install dsh` writes command.js
// and the MCP row, and only `deja install dsh-auto` adds auto.js. A profile can
// have the command from the installer and still want recall from here.
function cliPluginDir() {
  const home = process.env.DSH_HOME || join(homedir(), ".dsh");
  return join(home, "plugins", "deja");
}

function installedByCLI(file) {
  try {
    return existsSync(join(cliPluginDir(), file));
  } catch {
    return false;
  }
}

function apply(ctx, config) {
  const adds = contributions(
    { command: installedByCLI("command.js"), auto: installedByCLI("auto.js") },
    config,
  );
  if (adds.tools) tools(ctx);
  if (adds.command) command(ctx);
  if (adds.recall) autoRecall(ctx);
}

apply.inject = ["tools", "commands", "systemPrompt"];

export default apply;
