# DeepSeek Harness

- **ID**: `deepseek`
- **Store**: `${DSH_HOME:-~/.dsh}/sessions/<workspace-slug>/session-<uuid>/session.jsonl.zstd` — one append-only log per session
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
  speech.

`session/title` gives the session its name; when the model never answered, the
harness falls back to the first prompt.

- **MCP**: not wired. dsh loads plugins from its profile directories rather than
  a config file with an `mcpServers` root, so `deja install` has nothing to write
  yet.
- **Auto-recall**: no hook. The harness has a plugin runtime but no documented
  pre-prompt event deja can attach to.
- **Resume**: the tui app takes `--resume <session>` (a flag of the booted app,
  not of the launcher — `dsh --profile headless --resume` is rejected). deja
  stores the session id the flag wants, so the mapping is direct.
- **Handoff**: paste.

Format verified by installing dsh 0.1.1-rc.2, pointing it at a local model over
an OpenAI-compatible route, and reading what it wrote across sessions that
answered, called a tool, were interrupted mid-answer, and failed before
answering (`@deepseek-ai/dsh-session-persistence-jsonl`).

**Last verified:** 2026-08-21
