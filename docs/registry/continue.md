# Continue

- **ID**: `continue`
- **Store**: `${CONTINUE_GLOBAL_DIR:-~/.continue}/sessions/<sessionId>.json`, one document per session, with `sessions.json` beside them as the list.
- **Read override**: `DEJA_CONTINUE_ROOT`
- **Format**: whole-file JSON rewritten on change; full re-parse per pass.

Continue runs in VS Code and JetBrains, and its chat, agent and plan modes all
write the same file. The session document holds `history[]`, each item a
`message` with a `role` and a `content` that is either a string or an array of
`{type, text}` parts; an assistant item that called tools carries them in
`toolCallStates[]`. `system` and `tool` roles are skipped — the first is
configuration, and a tool's result arrives under the assistant item that asked
for it. The tool calls themselves are not indexed as work records: they are
model tools (`read_file`, `edit_file`) rather than commands anyone ran, and the
command index is for the latter.

Nothing in the file carries a timestamp. `sessions.json` records `dateCreated`
and `workspaceDirectory` per session, so that date is the session's start and
the file's mtime is its update; turns are laid out in order from the start,
which is enough to order them within the session. The project name comes from
`workspaceDirectory`, falling back to the list entry when the document omits it.

Shape verified against Continue's own types (`core/index.d.ts`: `Session`,
`ChatHistoryItem`, `ChatMessage`) and `core/util/paths.ts`; a live-store
validation is still welcome.

- **MCP**: not wired yet. Continue reads MCP servers from
  `~/.continue/config.yaml` under `mcpServers:`, which deja does not write.
- **Auto-recall**: none — Continue has no session-start, per-prompt or tool
  hook, so there is no event to answer.
- **Resume**: from Continue's history view in the editor; nothing takes a
  session id from a shell.
- **Handoff**: paste.

**Last verified:** 2026-09-07
