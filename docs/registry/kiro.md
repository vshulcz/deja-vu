# Kiro

- **ID**: `kiro`
- **Store (CLI)**: `~/.kiro/sessions/cli/<sessionId>.jsonl`, with the header `~/.kiro/sessions/cli/<sessionId>.json` beside it
- **Store (IDE)**: `~/.kiro/sessions/<workspace>/sess_<uuid>/messages.jsonl`, with `session.json` beside it
- **Store (headless CLI)**: `kiro-cli/data.sqlite3` in the local data directory (`~/Library/Application Support` on macOS, `~/.local/share` on Linux, `%LOCALAPPDATA%` on Windows), table `conversations_v2`
- **Read override**: `DEJA_KIRO_ROOT` replaces the session root, `DEJA_KIRO_DB` the database
- **Format**: JSONL, one shape per client, and a SQLite row per headless conversation
- **Needs**: `sqlite3` for the database only

Kiro (kiro.dev) ships a CLI and an IDE — a VS Code fork — and they do not write
the same file. The CLI stores a pair per session: a header naming the session id
and the directory it ran in, and a transcript whose records are
`{"kind":"Prompt"|"AssistantMessage","data":{…}}` with the text in
`data.content[].data` and `data.meta.timestamp` in seconds. The IDE stores one
directory per session under the workspace, holding `session.json` (id, model,
`workspacePaths`, timestamps) and `messages.jsonl`, whose lines carry
`payload.type` and `payload.content` with an RFC 3339 `timestamp`.

`kiro-cli chat --no-interactive` writes neither file. Each conversation is a
row of `conversations_v2` keyed by the directory it ran in, and the row's JSON
`history` holds the turns: a `Prompt` or `ToolUseResults` user side, a
`ToolUse` or `Response` assistant side. `kiro-cli chat --resume-id
<conversation_id>` reopens a row.

**Last verified:** 2026-10-08

## Known quirks and drift

- Resume: `cd <cwd> && kiro-cli chat --resume-id <sessionId>`, which needs
  Kiro CLI 2.2.0 or newer. kiro-cli finds a session by id from any directory,
  but runs it in the current one and rewrites the header's `cwd` to it, so the
  command runs in the directory the header names (#4305). With that directory
  gone the `cd` is left out and deja notes where it will run instead (#4460).
- **`sess_` is not only the IDE's.** `kiro-cli --v3` (2.22.0 ships the V3
  engine behind that flag) writes the same `<workspace>/sess_<uuid>` layout
  and adds each session to `~/.kiro/session-index/<workspace>.jsonl`. A
  session listed there resumes with `kiro-cli --v3 chat --resume-id
  sess_<uuid>` in its first `workspacePaths` directory; one that is not is the
  IDE's, reopens from the app, and `deja resume` says so (#4307). On a
  logged-in 2.22.0 TUI turn V3 writes the answer as `assistant` records with
  `operationType: "Say"`, the call as `tool_call` (`execute_bash` with
  `args.command`, `status`, `actionType`) and its output as `tool_result`;
  all three are read. Its thinking is an `assistant` record too, with
  `operationType: "Reasoning"`, `"..."` for content and the text encrypted
  in `reasoningSignature`: it is not an answer and is dropped.
  `session_start` carries the system prompt and `pending_interaction` /
  `interaction_resolved` the approval prompt; neither is a turn.
- **A reply arrives in pieces.** Several `AssistantMessage` records can share
  one `data.message_id`: the CLI appends the answer as it streams, each record
  carrying the next piece rather than the whole answer so far. Read one message
  per record and a recall quotes a third of a sentence, so a run under one id is
  joined in order. tokscale's reader sums the same records for the same reason
  (`crates/tokscale-core/src/sessions/kiro.rs`).
- **Two shapes in one IDE file.** Current builds write the `payload` wrapper;
  before that the same file held flat `{"role":…,"content":…}` lines. Both are
  read, so a store that predates the change is not lost.
- The IDE file also carries the agent's own bookkeeping — `session_metadata`,
  `usage_summary`, `turn_end`. Those are not turns: a record whose type is not
  one is dropped rather than attributed to a role, and a test pins that. A
  `tool_call` record is not a turn either, but it holds the call: `toolName`
  and `args` are read into commands (`execute_bash`, `execute_pwsh`), files,
  edits and written lines (`fs_write` and `fs_append` with `path` and `text`,
  `str_replace` with `oldStr`/`newStr`, `read_file`, `delete_file` with
  `targetFile`). The engine writes one such line per state an action passes
  through; only the one that says how it ended (`completed` or `failed`) is
  read, a `denied` call is not, and a failed edit keeps its file but not its
  sides (#4506).
- **Tool calls.** The TUI writes `toolUse` parts inside an
  `AssistantMessage` and the results as a `ToolResults` record; the database
  has `tool_uses` and `ToolUseResults`. The TUI names its tools `shell`,
  `write` and `read` (`content`, `oldStr`, `newStr`, `operations[].path`); the
  headless path keeps `execute_bash`, `fs_write` and `fs_read` (`file_text`,
  `old_str`, `new_str`). Both are read into commands, files, edits and tool
  output (#4299). A command carries `→ exit N` from its result's
  `exit_status` (#4505). Only the `Prompt` record carries a timestamp, so the reply
  and its calls take the prompt's.
- **Not read yet.** The IDE mirrors chats into its globalStorage
  (`kiro.kiroagent/<workspace>/*.chat` beside extensionless execution records).
  No sample is in hand, and a reader built against a guessed shape is one that
  drops history without saying so.
- Wiring: `deja install kiro` writes the server into
  `~/.kiro/settings/mcp.json`, which the CLI and the IDE both read — the same
  file `kiro-cli mcp add --scope global` writes. One thing the installer
  cannot do for you: a custom agent (`~/.kiro/agents/<name>.json`) reads the
  global servers only when it sets `"includeMcpJson": true`, and the install
  note says so. The agent `kiro-auto` writes sets it.
- Guidance: `deja install kiro` writes `~/.kiro/steering/deja.md`, the global
  half of steering — scanned for every project, alongside the workspace one.
  It is four lines on purpose. A steering document declares an inclusion mode,
  and the only mode measured as loaded by kiro-cli is `always`: `manual` is not
  loaded and cannot be invoked from a session, `fileMatch` was withheld
  (KiroCrew's steering reference, measured against 2.19.1). So this text is in
  front of every turn whether it is wanted or not, which is why it names the
  tool and stops rather than carrying the full skill deja writes where a skill
  is loaded on demand.
- Skill: `deja install kiro` also writes `~/.kiro/skills/deja-history/SKILL.md`.
  On kiro-cli 2.28 a skill there is listed in `disclose_context` in both
  engines (#4802).
- Command: `~/.kiro/prompts/deja.md`, a global prompt. V3 lists it in slash
  completion as `/deja`; V2 takes `@deja` or `/prompts get deja`. The 2.22.0
  TUI lists it as `/deja` ("Prompt from global") and sends the file as the user
  message with the arguments in it. The file has no frontmatter, since a
  prompt is sent as written.
- Global hooks: `deja install kiro-auto` writes `~/.kiro/hooks/deja.json`
  (`{"version":"v1","hooks":[…]}`): SessionStart runs `hook-context --plain`,
  UserPromptSubmit `hook-prompt --plain`, SessionEnd `hook-session-end`. The
  IDE and the `kiro-cli --v3` TUI run it in every chat, whatever the agent;
  `--v3 --no-interactive` runs none of it. Both outputs reach the model. On
  2.28 SessionEnd fires on leaving the TUI; 2.22.0 maps SessionEnd to Stop,
  so there it fires after every turn and not on `/quit`. A turn-end clear is
  harmless: the next prompt marks the session live again. PreToolUse and
  PostToolUse stdout is dropped
  (`sendStdout:false` in `acp-server.js`), so a pre-edit line or a fix for a
  failed command can only ride the next prompt, which waits on deferred
  delivery. The default V2 engine ignores this file and runs the agent below.
- Auto-recall: `deja install kiro-auto` writes the above and an agent of
  deja's own, `~/.kiro/agents/deja.json` (`tools: ["*"]`, `includeMcpJson`),
  with two hooks: `agentSpawn` runs `deja hook-context --plain` and
  `userPromptSubmit` runs `deja hook-prompt --plain`. kiro-cli adds what those
  print to the model's context, the agentSpawn output for the whole
  conversation; what `preToolUse` and `postToolUse` print is not sent. So
  two more hooks hold their answer for the next `userPromptSubmit`, which
  hands it over once: `preToolUse` runs `deja hook-tool --defer` for the line
  about a file `write`, `fs_write`, `fs_append` or `str_replace` edits, and
  `postToolUse` runs `deja hook-tool-after --defer` for the fix pair of a
  failed `shell` or `execute_bash`. Both arrive after the step they are about. An
  agent hook may name no session, so the working directory keys it. The hooks run in that agent only:
  `kiro-cli chat --agent deja`, or `kiro-cli agent set-default deja`. The
  built-in `kiro_default` takes no hooks from a file (a `kiro_default.json` is
  ignored), and deja does not switch `chat.defaultAgent`, which would trade
  the default agent's prompt for its own. `deja doctor` reads the row as
  `installed` until `chat.defaultAgent` is `deja`, and uninstall removes that
  setting when it names deja's agent. The IDE's `.kiro/hooks/` are a
  different system: per workspace, fired on file events (#4304).

## Measured on a live install

`kiro-cli` 2.22.0 with its own mock model (`KIRO_MOCK_CHAT_RESPONSE`), after
`deja install kiro-auto`: `kiro-cli chat --no-interactive --agent deja` sent a
request whose context carried the `<deja-recall>` digest naming the earlier
kiro session in that directory and its answer. A probe agent with all five
hooks echoing a marker showed the agentSpawn and userPromptSubmit markers in
the request and the preToolUse and postToolUse ones nowhere.

The 2.22.0 TUI (`kiro-cli chat --agent deja`, same mock, request bodies read
from its trace log): a `write` to a file two earlier sessions decided about
parked the pre-edit line, and the next prompt's request carried it in the
hook context ahead of the user message. A failed `shell` call did the same
with its fix pair. `/deja retry loop` sent `deja.md` as the user message with
the query in its command. A rule from `deja rules sync` reached the request
through `~/.kiro/steering/deja-rules.md`. `/quit` ran no SessionEnd: the V2
agent has none.

`kiro-cli` 2.22.0 (homebrew cask), in a hermetic HOME:

- `deja install kiro` writes `~/.kiro/settings/mcp.json` and
  `~/.kiro/steering/deja.md`, and says the one thing a reader has to know: a
  custom agent in `~/.kiro/agents/*.json` reads the global MCP servers only with `"includeMcpJson": true`.
- `--resume-id <SESSION_ID>` is in its own help, which is the command
  `deja resume` prints for a CLI session. `-r`, `--resume-picker` are beside it.
- The steering file is loaded: its text is in the request.

`kiro-cli` 2.22.0 logged in, real model, hermetic HOME holding only the login:

- `kiro-cli mcp list` lists `deja` for `kiro_default` and under global. In
  `kiro-cli chat --no-interactive --agent deja` the model called the `deja`
  tool (`mode: recall`) and answered from what it returned. The V3 TUI's
  default agent got `mcp_deja_deja` in its tool list and called it too.
- `kiro-cli --v3 chat` TUI, default agent: the first request carried the
  SessionStart digest and the UserPromptSubmit recall from
  `~/.kiro/hooks/deja.json`, and the model quoted both. `--v3 chat
  --no-interactive` ran no hook from that directory at all.
- `kiro-cli chat --agent deja` TUI: the digest reached the model, which
  quoted the earlier session's rule back before editing.
