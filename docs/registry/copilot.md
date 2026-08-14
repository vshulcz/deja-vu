# Copilot CLI

- **ID**: `copilot`
- **Store**: `~/.copilot/session-state/<sessionId>/events.jsonl`
- **Read override**: `DEJA_COPILOT_ROOT` (points at the `session-state` directory)
- **Format**: append-only JSONL, one event per line, each `{type, data, timestamp}`

`session.start` carries `sessionId` and `context.cwd`, which is where the
project name comes from; the directory the file sits in is not the id and can
differ from it. Conversation turns are `user.message` and `assistant.message`,
both with `data.content` as a plain string.

The work is filed outside the message stream. `tool.execution_start` carries
`toolName` and `arguments` — `path` for the file tools, `command` for `bash`,
and `old_str`/`new_str` for an edit, which is the one place the replaced text
survives. `tool.execution_complete` carries `data.result.content` and a
`success` flag; failed results are indexed on purpose, because the error a
command hit is what a later search reaches for. `session.shutdown` also lists
`codeChanges.filesModified`, the only harness that hands over a modified-file
list the parser does not have to infer.

- **MCP**: `deja install copilot` writes `mcpServers.deja` into
  `~/.copilot/mcp-config.json`.
- **Guidance**: a skill Copilot loads on demand.
- **Auto-recall**: none. Copilot CLI exposes no hook that can inject context,
  so MCP plus guidance is the whole install.
- **Resume**: `copilot --resume <sessionId>`.
- **Handoff**: exec.

## Known quirks and drift

- `assistant.message` records whose content is empty and whose work is entirely
  in tool calls used to be dropped; the tool events now carry them.
- Tool names are lowercase (`edit`, `read`, `write`, `bash`) where Claude Code
  capitalises, and the edit argument is `old_str` rather than `old_string`.
- A session directory can outlive its `events.jsonl`; the discovery walk only
  picks up files that exist.

Specified in [#655](https://github.com/vshulcz/deja-vu/issues/655), work
records added in [#1231](https://github.com/vshulcz/deja-vu/pull/1231).

**Last verified:** 2026-08-14
