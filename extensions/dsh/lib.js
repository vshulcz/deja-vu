// The plugin's decision, kept out of index.js because a host is free to treat
// anything a plugin module exports as part of the plugin — opencode does
// exactly that with its own — and this is a helper, not a second plugin.

// argv builds the call for a query somebody typed, rather than handing the text
// to deja as it stands.
//
// The command is always named. deja's bare-query path dispatches a first word
// that happens to be a command, so a one-word query went to the command of that
// name: `version` printed a version number, `index` rebuilt the index, and
// neither searched anything.
//
// A query that starts with a dash is read as a flag: `deja search --no-verify`
// exits on an unknown flag, the caller turns that into "", and the model is
// told the history holds nothing about --no-verify while eleven sessions
// discuss it — a confident wrong answer, which is worse than an error. `--`
// ends the flags. It is sent only when the query needs it, so a deja too old to
// know the terminator on this subcommand still answers every ordinary query.
export function argv(cmd, flags, text) {
  const arg = String(text);
  return arg.startsWith("-") ? [cmd, ...flags, "--", arg] : [cmd, ...flags, arg];
}

// recallArgv is the call behind the deja_recall tool: `deja recall`, the page
// the MCP recall tool serves, framed and held to its 4 KB budget by deja
// itself. The tool used to run `search --json`, which carries every hit's
// matched messages whole and handed a model 661,528 characters for five hits
// (#5568). The page ends with "call recall again with offset=N"; offset is
// how the tool asks for that, and it is sent only past the first page, so a
// deja too old to know --offset still answers the first one.
export function recallArgv(args) {
  const count = (v, fallback) => {
    const n = Math.trunc(Number(v));
    return v == null || v === "" || !Number.isFinite(n) ? fallback : n;
  };
  const limit = Math.min(20, Math.max(1, count(args && args.limit, 5)));
  const offset = Math.min(1e9, Math.max(0, count(args && args.offset, 0)));
  const flags = ["--limit", String(limit)];
  if (offset > 0) flags.push("--offset", String(offset));
  return argv("recall", flags, args && args.query);
}

// recallSilence is the answer when `deja recall` printed nothing. Past the
// first page that is a deja older than --offset refusing the flag, not an
// empty history: recall always prints a line, "No more matches" included.
export function recallSilence(call) {
  if (call.includes("--offset")) {
    return "This deja cannot page recall. Run deja update, then ask again with the same offset.";
  }
  return "Nothing in this machine's history matches that.";
}

// guarded runs one registration, and swallows the host's refusal of a name it
// already holds.
//
// dsh answers a duplicate with "prompt context deja:recall is already
// registered" or "command deja is already registered", and that failure is not
// local: the whole plugin tree fails to load, so a second copy of this plugin —
// the npm package next to the installer's files, a --patch overlay naming it
// twice — costs the user their agent over an optional memory plugin. One
// registration is all any of these need, so the loser stands down.
export function guarded(register) {
  try {
    register();
    return true;
  } catch {
    return false;
  }
}

// resultText is the text a tool handed back to the model.
export function resultText(result) {
  const content = result && result.content;
  if (!Array.isArray(content)) return "";
  return content
    .filter((part) => part && part.type === "text" && typeof part.text === "string")
    .map((part) => part.text)
    .join("\n");
}

// exitStatus reads how a dsh command ended. dsh reports it in the text rather
// than as an error result: a last line of "[exit code: N]" for a non-zero exit,
// or "[killed by signal: X]". Only the last line counts, since output may quote
// a marker anywhere else. The marker is cut from the body before the lookup:
// an error whose last line is "[exit code: 1]" matches nothing on file.
export function exitStatus(text) {
  const signal = /\n\[killed by signal: [^\]\n]+\]$/.exec(text);
  if (signal) return { body: text.slice(0, signal.index), failed: true };
  const exit = /\n\[exit code: (\d+)\]$/.exec(text);
  if (exit) return { body: text.slice(0, exit.index), failed: exit[1] !== "0" };
  return { body: text, failed: false };
}

// inert keeps recalled text out of dsh's prompt templating. dsh reads every
// {{name}} in a context as a prompt variable and throws on one it does not
// know, which ends the turn before the model is called. Past sessions quote
// templates, and that very error, so one recall carrying {{cwd}} broke every
// turn after it (#5421). `interpolate: false` tells dsh 0.2.1-alpha.2 and later
// to leave the text alone; older hosts ignore it, and a listener on
// system-prompt/assemble may drop it, so the braces are split as well:
// { {cwd} } still reads as braces and holds no {{ to resolve.
export function inert(text) {
  return String(text || "").replace(/\{(?=\{)/g, "{ ").replace(/\}(?=\})/g, "} ");
}

// contributions says what this package adds, given what `deja install` already
// wrote into DSH_HOME and what the profile turned off. The rule is the same
// everywhere: fill the gaps, never repeat the installer.
//
// command.js and the mcp-deja profile row are written by the same install, so
// the command file standing there means the model can already reach deja —
// registering these tools would list every answer twice. auto.js is separate:
// only `deja install dsh-auto` writes it, so a profile can take the command
// from the installer and still want recall from here.
export function contributions(wiring, config) {
  const wired = wiring || {};
  const settings = config || {};
  return {
    tools: !wired.command,
    command: !wired.command,
    recall: settings.autoRecall !== false && !wired.auto,
  };
}

// newestEvents walks a dsh session's log from the end, whichever shape the
// host gives it. 0.1.x exposes the log as session.events; 0.2.0 made the array
// private and reads one event at a time with eventAt(seq), seq being the next
// free position. Reading only session.events found nothing on 0.2.0, so the
// per-prompt recall never ran there.
export function* newestEvents(session) {
  if (!session) return;
  if (Array.isArray(session.events)) {
    for (let i = session.events.length - 1; i >= 0; i--) yield session.events[i];
    return;
  }
  if (typeof session.eventAt !== "function") return;
  let top;
  try {
    top = Number(session.seq);
  } catch {
    return;
  }
  if (!Number.isSafeInteger(top)) return;
  for (let i = top - 1; i >= 0; i--) {
    let event;
    try {
      event = session.eventAt(i);
    } catch {
      return;
    }
    yield event;
  }
}

// splitHookReply reads what `deja hook-prompt` or `deja hook-context` printed
// in its JSON form and splits it by reader: additionalContext is for the model,
// the same text --plain prints, and systemMessage is the line Claude Code
// shows the person. One spawn answers both.
//
// The envelope is the last line that parses as one, so a stray line printed
// ahead of it does not carry the person's line into the model's context with
// it. Output with no envelope at all is all context, so a deja that printed
// plain text still reaches the model.
export function splitHookReply(stdout) {
  const text = String(stdout || "").trim();
  if (!text) return { context: "", notes: "" };
  const lines = text.split("\n");
  for (let i = lines.length - 1; i >= 0; i--) {
    const line = lines[i].trim();
    if (!line.startsWith("{")) continue;
    let reply;
    try {
      reply = JSON.parse(line);
    } catch {
      continue;
    }
    if (!reply || typeof reply !== "object" || Array.isArray(reply)) continue;
    if (!("hookSpecificOutput" in reply) && !("systemMessage" in reply)) continue;
    const out = reply.hookSpecificOutput;
    const context = out && typeof out.additionalContext === "string" ? out.additionalContext.trim() : "";
    const notes = typeof reply.systemMessage === "string" ? reply.systemMessage.trim() : "";
    return { context, notes };
  }
  return { context: text, notes: "" };
}

// noteBoard holds deja's lines for the person until the page asks for them.
// The recall runs in the host and the person reads a browser, so each
// session's lines wait here and are handed over once: take() empties the
// session's list. Two tabs on one session race for them, and the first to ask
// gets them; the other shows nothing, which is the same once rule deja keeps.
//
// Bounded three ways, because a page that never asks must not grow the host's
// memory, and a session reopened tomorrow must not open on yesterday's receipt:
// lines per session, sessions held, and how long a line waits.
export function noteBoard({ perSession = 8, sessions = 64, ttlMs = 10 * 60 * 1000, now = Date.now } = {}) {
  const board = new Map();
  let reader = false;
  return {
    push(sid, text) {
      const at = now();
      const lines = String(text || "")
        .split("\n")
        .map((line) => line.trim())
        .filter(Boolean)
        .map((line) => ({ line, at }));
      if (!sid || lines.length === 0) return;
      const held = board.get(sid) || [];
      board.delete(sid);
      board.set(sid, [...held, ...lines].slice(-perSession));
      while (board.size > sessions) board.delete(board.keys().next().value);
    },
    take(sid) {
      const held = board.get(sid) || [];
      board.delete(sid);
      const fresh = now() - ttlMs;
      return held.filter((n) => n.at >= fresh).map((n) => n.line);
    },
    // Whether anything will ever read the board. deja marks its notes shown
    // when it prints them, so asking for them with no page to show them on
    // spends them: `/deja` and the next Claude Code session would not see
    // them either.
    hasReader: () => reader,
    setReader: () => {
      reader = true;
    },
  };
}
