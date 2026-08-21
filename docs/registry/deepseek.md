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

Three event types carry the conversation:

- `user/message` with `data.source.kind == "user"` is what a person typed. The
  same type also carries what plugins splice into the turn — the sandbox policy
  snapshot, the skill catalogue — under other source kinds, and those are the
  harness describing itself rather than history worth recalling.
- `assistant/chunk` with `data.chunk.type == "text-delta"` holds the answer a
  token at a time; the deltas of one run are joined into a single message.
- `assistant/chunk` with `data.chunk.type == "tool-result"` is tool output, kept
  under the `tool-output` role so search can tell it from speech.

`session/title` gives the session its name; when the model never answered, the
harness falls back to the first prompt.

- **MCP**: not wired. dsh loads plugins from its profile directories rather than
  a config file with an `mcpServers` root, so `deja install` has nothing to write
  yet.
- **Auto-recall**: no hook. The harness has a plugin runtime but no documented
  pre-prompt event deja can attach to.
- **Resume**: dsh's own `--resume <session>`; deja stores the session id, and the
  mapping is direct.
- **Handoff**: paste.

Format verified by installing dsh 0.1.1-rc.2, running it, and reading what it
wrote (`@deepseek-ai/dsh-session-persistence-jsonl`).

**Last verified:** 2026-08-21
