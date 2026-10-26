// dsh-deja — the history you already have, inside DeepSeek Harness.
//
// dsh answers questions about its own sessions through the built-in
// session-query subsystem. This plugin answers the other question: what you did
// in Claude Code, Codex, Cursor, opencode and thirty-nine more agents, on this
// machine, before dsh existed. The index is deja's; this file is the seam.

import { createRequire } from "node:module";
import { execFile, execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { existsSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

import { argv, contributions, recallArgv, recallSilence, exitStatus, guarded, inert, newestEvents, noteBoard, resultText, splitHookReply } from "./lib.js";

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
        windowsHide: true,
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
function run(args, input, cwd) {
  try {
    return execFileSync(DEJA, args, {
      windowsHide: true,
      cwd,
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
  // declaration serves them all. Printed with --framed: ctx, blame, fix and
  // how write for a person (recall frames its own page), and without the frame a tag planted in a
  // transcript reached the model as though deja had closed its own block. The schema is plain JSON Schema — a
  // schemastery instance is rejected as "schema must be a value schema object",
  // because the host validates that this is an ordinary JSON record.
  const TEXT_OUTPUT = {
    schema: { type: "string" },
    render: (_args, value) => [{ type: "text", text: String(value) }],
  };

  guarded(() => ctx.tools.register(defineTool({
    name: "deja_recall",
    description:
      "Search this machine's own past AI coding sessions — every agent used on it, including months before deja was installed. Use before debugging an error or re-implementing anything that may already exist, and before the first edit in a task and before calling a change done or ready to merge. Match on the most specific token available: an exact error string, function name, file path or flag.",
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
      offset: {
        type: "number",
        description: "Sessions to skip, from the offset the previous page named. Default 0.",
      },
    },
    output: TEXT_OUTPUT,
    execute(args, exec) {
      // The page the MCP recall tool serves, framed and budgeted by deja. It
      // puts the project of its working directory first, so it runs in the
      // calling session's workspace rather than wherever dsh was launched.
      const call = recallArgv(args);
      const out = run(call, undefined, sessionCwd(exec && exec.agent));
      return Promise.resolve(answer(out || (INSTALLED ? recallSilence(call) : "")));
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
      return Promise.resolve(answer(run(["--framed", ...argv("ctx", [], args.query)])));
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
      return Promise.resolve(answer(run(["--framed", ...argv("blame", ["--json"], args.path)])));
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
      return Promise.resolve(answer(run(["--framed", ...argv("fix", [], args.error)])));
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
      return Promise.resolve(answer(run(["--framed", ...argv("how", [], args.what)])));
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

function command(ctx, board) {
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
      if (!INSTALLED) return { kind: "error", text: MISSING };
      // Outside dsh web a person sees nothing the hooks print — the digest
      // goes to the model — so the reply to /deja is where deja's own notes
      // reach them. In dsh web the session's lines may still be waiting on the
      // board for a page that has not asked; they go here, once, instead.
      const waiting = board ? board.take(sessionId(invocation && invocation.agent)) : [];
      const notes = [...waiting, run(["hook-context", "--notes"])].filter(Boolean).join("\n");
      return { kind: "success", text: notes ? answer(out) + "\n\n" + notes : answer(out) };
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
// autoDigest puts what this project settled in front of the model once at the
// start of the session — the question-independent counterpart to autoRecall.
// dsh's own agent/session-start is emit-only, its return reaches nothing, so
// the digest rides the same assembly seam the recall does. deja_once keys the
// one-shot on the session id in deja's ledger, which survives a resume in a new
// process; the Set is the in-process guard that keeps the long-lived web and
// tui profiles from spawning deja on every assembly of a session already shown.
function autoDigest(ctx, board) {
  const seen = new Set();

  guarded(() =>
    ctx.systemPrompt.context({
      name: "deja:project",
      order: 110,
      interpolate: false,
      text: (assembly) => {
        const agent = assembly && assembly.agent;
        if (!agent) return "";
        const sid = sessionId(agent);
        if (sid && seen.has(sid)) return "";
        if (sid) seen.add(sid);
        return inert(askHook(
          ["hook-context", "--plain"],
          ["hook-context"],
          JSON.stringify({ session_id: sid, cwd: sessionCwd(agent), source: "startup", deja_once: true }),
          sid,
          board,
        ));
      },
    }),
  );
}

// dsh names a session "session-<uuid>" and its log's folder the same; deja
// indexes it by the uuid, so that is the id every hook is given.
function bareSessionId(id) {
  return String(id || "").replace(/^session-/, "");
}

function sessionId(agent) {
  return bareSessionId((agent && (agent.sessionId || (agent.session && agent.session.id))) || "");
}

// The workspace a session belongs to. One web or tui process serves sessions
// from every workspace and never changes directory, so process.cwd() is only
// where dsh was launched; the session header carries the directory the session
// was opened in.
function sessionCwd(agent) {
  const header = agent && agent.session && agent.session.header;
  return (header && header.cwd) || process.cwd();
}

// askHook runs a hook once for both readers. Where a page will show the
// person's lines, deja is asked for its JSON reply: additionalContext, the
// text --plain prints, goes to the model and systemMessage waits on the board.
// Anywhere else — tui, headless, sdk — it stays --plain. deja marks a note
// shown when it prints it, so asking for notes nobody will read spends them,
// and `/deja` and the next Claude Code session would not see them either.
function askHook(plain, json, payload, sid, board) {
  if (!board.hasReader()) return run(plain, payload);
  const reply = splitHookReply(run(json, payload));
  board.push(sid, reply.notes);
  return reply.context;
}

function autoRecall(ctx, board) {
  let asked = "";
  let askedIn = "";
  let askedBy = "";
  let recalled = "";
  const compacted = new Set();
  watchCompaction(ctx, compacted);

  guarded(() =>
    ctx.systemPrompt.context({
      name: "deja:recall",
      order: 120,
      interpolate: false,
      text: (assembly) => {
        const agent = assembly && assembly.agent;
        if (!agent) return "";
        const prompt = lastHumanText(agent);
        if (!prompt) return "";
        const cwd = sessionCwd(agent);
        const sid = sessionId(agent);
        const afterCompaction = compacted.delete(sid);
        // The same question asked in another workspace is a different question,
        // and so is one asked again after its session compacted: deja then
        // answers with the recovery packet, which only a prompt naming the
        // session gets back.
        if (prompt !== asked || cwd !== askedIn || sid !== askedBy || afterCompaction) {
          asked = prompt;
          askedIn = cwd;
          askedBy = sid;
          recalled = inert(askHook(
            ["hook-prompt", "--plain"],
            ["hook-prompt"],
            JSON.stringify({ prompt, cwd, session_id: sid }),
            sid,
            board,
          ));
        }
        // Silence is the common case: this speaks only when the history answers.
        return recalled;
      },
    }),
  );
}

// The person's half. dsh shows nobody what a plugin hands the model, and has
// no notice channel a host plugin can write to, so deja's lines wait on a board
// and the browser half (client.js) asks for them under the composer. The route
// rides dsh's own /api mount, which applies the same Host/Origin fence and
// browser login as every other call the page makes. Only a profile that serves
// that page — the connection service and a web server both — registers it;
// headless, tui, sdk and an older dsh never do, and keep asking deja for
// --plain. A second copy of the plugin finds the path taken and stands down.
const NOTES_PATH = "/api/deja-notes";

function notesRoute(ctx, board) {
  if (typeof ctx.inject !== "function") return;
  try {
    ctx.inject(["connection", "webServer"], (c) => {
      const registered = guarded(() =>
        c.connection.fetch.register({
          path: NOTES_PATH,
          methods: ["GET"],
          requestBody: "buffered",
          fetch: async (request) => {
            let sid = "";
            try {
              sid = bareSessionId(new URL(request.url).searchParams.get("session"));
            } catch {}
            return Response.json({ notes: sid ? board.take(sid) : [] }, { headers: { "cache-control": "no-store" } });
          },
        }),
      );
      if (registered) board.setReader();
    });
  } catch {}
}

// runAsync is run for a seam dsh awaits, so the event loop goes on while deja
// answers. It never rejects.
function runAsync(args, input) {
  return new Promise((resolve) => {
    try {
      const child = execFile(DEJA, args, { windowsHide: true, encoding: "utf8", timeout: 20000, maxBuffer: 8 * 1024 * 1024 }, (err, stdout) =>
        resolve(err ? "" : String(stdout || "").trim()),
      );
      child.on("error", () => resolve(""));
      child.stdin.on("error", () => {});
      child.stdin.end(input);
    } catch {
      resolve("");
    }
  });
}

// Compaction shadows the turns it summarises but leaves them in the session
// log, so deja reads the session there as compaction/start lands and keeps a
// recovery packet: the task, each command with how it went, what was still
// open. The log is written behind the session, so it is flushed first. dsh
// assembles a step's context before its agent/pre-step waterfall, where the
// compaction runs, so the step that compacts waits there for the capture and
// the packet rides the recall of the next step, or of the next turn.
function watchCompaction(ctx, compacted) {
  if (typeof ctx.on !== "function") return;
  const capturing = new Map();
  const listen = (c) =>
    c.on("session/event", (session, event) => {
      if (!session || !event || event.type !== "compaction/start") return;
      const sid = bareSessionId(session.id || (session.header && session.header.id));
      if (!sid) return;
      const cwd = (session.header && session.header.cwd) || process.cwd();
      // One compaction can follow another inside a step; two captures at once
      // contend for the index and the loser marks the packet unavailable, so
      // each waits for the one before it.
      const before = capturing.get(sid) || Promise.resolve();
      const capture = before.then(async () => {
        try {
          if (c.sessions && typeof c.sessions.flush === "function") await c.sessions.flush(session);
        } catch {}
        await runAsync(["hook-precompact"], JSON.stringify({ session_id: sid, cwd, harness: "deepseek" }));
        compacted.add(sid);
      });
      capturing.set(sid, capture);
      capture.then(() => {
        if (capturing.get(sid) === capture) capturing.delete(sid);
      });
    });
  try {
    if (typeof ctx.inject === "function") ctx.inject(["sessions"], listen);
    else listen(ctx);
  } catch {}
  try {
    ctx.on("agent/pre-step", async (step, next) => {
      const sid = sessionId(step && step.agent);
      if (capturing.has(sid)) await capturing.get(sid);
      const decision = await next();
      if (capturing.has(sid)) await capturing.get(sid);
      return decision;
    });
  } catch {}
}

// The point of action, as `deja install dsh-auto` wires it. tools/post-execute
// runs on every tool result, and an accept decision's additionalContexts reach
// the model on the next step: a read, edit or write gets what deja knows about
// the file, a bash that exited non-zero gets the fix this machine found for
// that error before. The before-seam, tools/execute, has no channel to the
// model.
function toolNotes(ctx) {
  if (typeof ctx.on !== "function") return;
  try {
    ctx.on("tools/post-execute", async (exec, result, next) => {
      const decision = await next();
      try {
        if (!decision || decision.kind !== "accept" || !exec) return decision;
        const note = await toolNote(exec, result);
        if (!note) return decision;
        return {
          ...decision,
          additionalContexts: [...(decision.additionalContexts || []), noteMessage(note)],
        };
      } catch {
        return decision;
      }
    });
  } catch {}
}

// A session is over when dsh disposes of it, or when the process goes: on
// 0.1.1-rc.2 the headless profile exits without disposing anything, and only
// process "exit" runs. The digest and the recall stamp a session live, which
// keeps it out of its own MCP recall; dropping the stamp lets the next session
// be answered with it now rather than twenty minutes from now.
function sessionEnds(ctx) {
  if (typeof ctx.on !== "function") return;
  // Each live session with the directory it opened in: the end refreshes that
  // project's start digest, so the next session there sees this one.
  const live = new Map();
  const end = (sid) => {
    if (!sid || !live.has(sid)) return;
    const cwd = live.get(sid);
    live.delete(sid);
    run(["hook-session-end"], JSON.stringify({ session_id: sid, cwd }));
  };
  try {
    ctx.on("session/created", (session) => {
      const sid = bareSessionId(session && session.id);
      if (sid) live.set(sid, (session.header && session.header.cwd) || process.cwd());
    });
    ctx.on("session/disposed", (session) => end(bareSessionId(session && session.id)));
    process.on("exit", () => {
      for (const sid of [...live.keys()]) end(sid);
    });
  } catch {}
}

async function toolNote(exec, result) {
  const args = exec.arguments || {};
  const base = { session_id: sessionId(exec.agent), cwd: sessionCwd(exec.agent) };
  if ((exec.name === "read" || exec.name === "edit" || exec.name === "write") && args.file_path) {
    return runAsync(["hook-tool", "--plain"], JSON.stringify({
      tool_name: exec.name,
      tool_input: { file_path: String(args.file_path) },
      ...base,
    }));
  }
  // The profile's other editor names the file "path"; view is its read.
  if (exec.name === "str_replace_editor" && args.path) {
    return runAsync(["hook-tool", "--plain"], JSON.stringify({
      tool_name: args.command === "view" ? "read" : "edit",
      tool_input: { file_path: String(args.path) },
      ...base,
    }));
  }
  // pwsh is the Windows shell, with the same markers.
  if (exec.name === "bash" || exec.name === "pwsh") {
    const status = exitStatus(resultText(result));
    if (!(result && result.isError) && !status.failed) return "";
    return runAsync(["hook-tool-after", "--plain"], JSON.stringify({
      tool_name: exec.name === "pwsh" ? "powershell" : "bash",
      tool_input: { command: String(args.command || "") },
      tool_response: status.body,
      ...base,
    }));
  }
  return "";
}

// A context message the way dsh's own plugins build one: user role, a
// producer-owned source so it is not read back as something the person typed,
// a fresh id, frozen. v4 session format refuses the bare "plugin" kind, so the
// producer owns "plugin:deja" — the value dsh's own v3→v4 migration assigns an
// unmapped plugin.
function noteMessage(text) {
  const message = {
    role: "user",
    content: [{ type: "text", text }],
    source: { kind: "plugin:deja", form: "notice" },
    id: randomUUID(),
  };
  Object.freeze(message.content[0]);
  Object.freeze(message.content);
  Object.freeze(message.source);
  return Object.freeze(message);
}

// lastHumanText is the newest thing the person actually typed. At assembly
// time the message has already left the inbox and has not been appended as a
// "user/message" yet — the only durable record of it is the inbox splice that
// carried it in, so both are read. Anything a plugin contributed is skipped:
// those carry a source of their own.
function lastHumanText(agent) {
  for (const event of newestEvents(agent && agent.session)) {
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
  const board = noteBoard();
  if (adds.tools) tools(ctx);
  if (adds.command) command(ctx, board);
  if (adds.recall) {
    autoDigest(ctx, board);
    autoRecall(ctx, board);
    notesRoute(ctx, board);
    toolNotes(ctx);
    sessionEnds(ctx);
  }
}

apply.inject = ["tools", "commands", "systemPrompt"];

export default apply;
