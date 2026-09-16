# Kiro

- **ID**: `kiro`
- **Store (CLI)**: `~/.kiro/sessions/cli/<sessionId>.jsonl`, with the header `~/.kiro/sessions/cli/<sessionId>.json` beside it
- **Store (IDE)**: `~/.kiro/sessions/<workspace>/sess_<uuid>/messages.jsonl`, with `session.json` beside it
- **Read override**: `DEJA_KIRO_ROOT` replaces the session root
- **Format**: JSONL, one shape per client
- **Needs**: nothing

Kiro (kiro.dev) ships a CLI and an IDE — a VS Code fork — and they do not write
the same file. The CLI stores a pair per session: a header naming the session id
and the directory it ran in, and a transcript whose records are
`{"kind":"Prompt"|"AssistantMessage","data":{…}}` with the text in
`data.content[].data` and `data.meta.timestamp` in seconds. The IDE stores one
directory per session under the workspace, holding `session.json` (id, model,
`workspacePaths`, timestamps) and `messages.jsonl`, whose lines carry
`payload.type` and `payload.content` with an RFC 3339 `timestamp`.

**Last verified:** 2026-09-16

## Known quirks and drift

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
  `usage_summary`, `turn_end`, `tool_call`. Those are not turns: a record whose
  type is not one is dropped rather than attributed to a role, and a test pins
  that.
- **Not read yet.** The IDE mirrors chats into its globalStorage
  (`kiro.kiroagent/<workspace>/*.chat` beside extensionless execution records)
  and the TUI keeps sessions in `kiro-cli/data.sqlite3`, table
  `conversations_v2`. No sample of either is in hand — Kiro is not installed on
  the machine this was written on — and a reader built against a guessed shape
  is one that drops history without saying so.
- Read support only. Kiro takes MCP servers in `~/.kiro/settings/mcp.json` and
  agent hooks in `.kiro/hooks/`, so the wiring exists to be written; the
  registry records each as a gap rather than leaving it blank.
