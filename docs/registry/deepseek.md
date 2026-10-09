# DeepSeek Harness

- **ID**: `deepseek`
- **Store**: `${DSH_HOME:-~/.dsh}/sessions/<workspace-slug>/session-<uuid>/session.v4.jsonl.zstd`, one append-only log per session. The number is the format generation: earlier dsh builds wrote `session.jsonl.zstd` and `session.v3.jsonl.zstd`, and when dsh opens an older session it writes the whole history into a new-generation file and leaves the old one beside it, unchanged. deja reads the newest generation in each session directory and accepts any `session.vN.jsonl`, raw or zstd-framed, so the next bump is read rather than skipped
- **Read override**: `DEJA_DEEPSEEK_ROOT` (sessions root), `DSH_HOME` (the harness's own home, also honored)
- **Format**: JSONL, written as consecutive zstd frames by default; raw lines
  are a configuration and both are read
- **Prerequisite**: the `zstd` CLI, for the same reason Zed needs it. Without
  it the sessions are found and none of them can be read, and `deja index` says
  so rather than reporting an empty store.

The first line is the session header — `{"type":"session","id":"session-<uuid>",
"createdAt":<ms>,"cwd":"…"}` — and the project comes from that `cwd`. Every
line after it is one event: `{type, seq, time, data}`.

These event types carry the conversation:

- `user/message` with `data.source.kind == "user"` is what a person typed. The
  same type also carries what plugins splice into the turn — the sandbox policy
  snapshot, the skill catalogue — under other source kinds, and those are the
  harness describing itself rather than history worth recalling.
- `assistant/message` is the agent's turn, complete: `data.message.content` is a
  block array of `text` and `reasoning`. Only the text is recalled; reasoning is
  the model thinking out loud rather than what it told the person.
- `assistant/chunk` with `chunk.type == "text-delta"` is the same answer as it
  streamed. It is read only as a fallback, for a run interrupted before the
  complete message landed — otherwise the answer would land twice.
- `text-chunks` is a packed row: a run of three or more consecutive deltas
  stored as one line with the pieces in `data.texts`. A reader that knows only
  `assistant/chunk` keeps the stray deltas and loses every long answer, which is
  exactly the interrupted case the fallback exists for.
- `tool/result` is tool output, whose content nests a `tool-result` block around
  the text; it is kept under the `tool-output` role so search can tell it from
  speech. From v4 the message is the result itself: `role: "tool"`, the text
  blocks as its content and `isError` on the message, which is where a refused
  edit is read from.

`session/title` gives the session its name; when the model never answered, the
harness falls back to the first prompt.

- **MCP**: `deja install deepseek` writes `$DSH_HOME/cordis.patch.yml`, the
  home-level patch layer every profile composes over its own. An MCP server here
  is a plugin row (`@deepseek-ai/dsh-mcp-client`) inside an `insert:` list — a
  bare row is rejected with `entry "mcp-deja" not found`, because a patch entry
  addresses a row that already exists. After it dsh lists one tool, `deja`,
  called with a `mode` of `recall`, `context`, `blame`, `fix`, `how`,
  `orient`, `remember` or `handoff`.
- **Skill**: the shared `~/.agents/skills/deja-history/SKILL.md`. dsh splices a
  skill catalogue into the turn and reads that directory, so it needs no file of
  its own — checked by asking a running dsh to list its skills.
- **Command**: `deja install deepseek` also writes a plugin at
  `$DSH_HOME/plugins/deja/command.js` and names it from the same layer, because
  dsh registers slash commands in code (`ctx.commands.register`) rather than
  from a directory of markdown. A profile row may name an absolute path, which
  is how deja ships one without publishing a package. Three details fail the
  whole profile load rather than skipping the plugin, and each was found by
  running it: the dependency is declared as `apply.inject`, the field is
  `handler` and not `handle`, and a row naming a file that is not there yet
  takes the profile down — so the plugin is written before the layer and the
  layer is rewritten before the plugin is removed. The command plane itself is
  a UI service, so `/deja` lives in the web profile; a headless run has no
  command adapter, and what a headless boot proves is that the plugin registers
  cleanly.
- **Auto-recall**: `deja install deepseek-auto` writes a second plugin at
  `$DSH_HOME/plugins/deja/auto.js`. Recall arrives through
  `ctx.systemPrompt.context`, evaluated on every assembly. The obvious
  alternative — a middleware on `agent/pre-step` that splices a message into
  the step — loads, completes the turn and never reaches the model: a later
  listener rebuilds its answer from the payload and the added message is
  dropped with nothing reported. The plain
  `deja install deepseek` target keeps the MCP server and `/deja` but writes no
  such plugin, and removes it along with its row when someone drops back to it.
  Verified against a local model with no tools in play: dsh answered a question
  about a pool size that only the injected block carried.
  The same plugin listens on `tools/post-execute`, which runs on every tool
  result: after a `read`, `edit`, `write` or `str_replace_editor` call it adds
  `deja hook-tool`'s line about the file, and after a `bash` or `pwsh` whose result ends in `[exit code: N]` or `[killed by signal: X]`
  (the last line only, as dsh's own parser reads it) it adds
  `deja hook-tool-after`'s earlier fix for that error, both as
  `additionalContexts`, which dsh hands the model on the next step. The marker
  is dropped before the lookup, as the index drops it. Measured on dsh
  0.1.1-rc.2: a bash that failed with an error two earlier sessions had fixed
  was followed, in the next request, by a `<deja-recall>` message naming the
  command that fixed it. `tools/execute`, the seam before the call, has no
  channel to the model (#4293). A session the plugin saw created is ended
  with `deja hook-session-end` at `session/disposed`, or at process exit,
  since headless 0.1.1-rc.2 exits without disposing it. A `compaction/start`
  session event runs `deja hook-precompact` on the flushed log, and the packet
  rides the recall of the next step.
  The workspace deja is asked about is the session's, read from the session
  header (`agent.session.header.cwd`). One `dsh web` process serves sessions
  from every workspace and never changes directory, so `process.cwd()` is only
  where dsh was launched and is kept as the fallback. The plugin directory also
  holds a `package.json` declaring `"type": "module"`: Node takes a file's
  module type from the nearest `package.json` above it, and a home directory
  whose own `package.json` declares CommonJS otherwise makes dsh refuse both
  plugins with "Failed to load the ES module".
- **Resume**: none. The launcher's examples mention a tui profile taking
  `--resume <session>`, but this release ships no bundle for one — the two apps
  are `headless`, which takes a task and exits, and `web`, whose flags are all
  about the server. Reopening a conversation is something the web sidebar does,
  so there is nothing for deja to print.
- **Handoff**: exec, `dsh --profile headless <prompt>` answers it once and exits.

Format verified by installing dsh 0.1.1-rc.2, pointing it at a local model over
an OpenAI-compatible route, and reading what it wrote across sessions that
answered, called a tool, were interrupted mid-answer, and failed before
answering (`@deepseek-ai/dsh-session-persistence-jsonl`). The v4 generation
was checked against the source of `@deepseek-ai/dsh-base` 0.1.7-rc.2 (dsh
2.0.15): `dsh-session-format` names generation N `session.vN.jsonl`, and
`dsh-session-format-v3-to-v4` changes the tool-result shape and namespaces
plugin sources and block types, leaving the event envelope as it was.

**Last verified:** 2026-10-02
