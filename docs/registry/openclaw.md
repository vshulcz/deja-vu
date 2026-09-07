# OpenClaw

- **ID**: `openclaw`
- **Store**: `${OPENCLAW_STATE_DIR:-~/.openclaw}/agents/<agentId>/agent/openclaw-agent.sqlite` since OpenClaw 2026.8 — session rows and transcript events in one per-agent SQLite database; before that, `agents/<agentId>/sessions/<sessionId>.jsonl`, one append-only pi-format transcript per session, which the upgrade migrates into the database and leaves behind as an archive
- **Read override**: `DEJA_OPENCLAW_ROOT` (agents root), `OPENCLAW_STATE_DIR` (OpenClaw's own state override, also honored)
- **Format**: SQLite (`transcript_events.event_json`, one pi-format line per row, `session_windows` marking reset and rollover boundaries) or JSONL, append-cheap incremental parse from offset

OpenClaw's agent runtime is pi-lineage, so transcripts share pi's line shape:
a `{"type":"session"}` header (id, timestamp, optional cwd) followed by
`{"type":"message"}` entries whose `message.content` is a block array. The
shared pi parser handles both; when the header carries a `cwd`, it becomes
the project key, otherwise sessions attribute to `openclaw-<agentId>`.

The SQLite flip (openclaw/openclaw#98236, in 2026.8.x) moved the runtime
store into `agent/openclaw-agent.sqlite`: `transcript_events` holds the same
lines the JSONL held, keyed by session id and sequence, and `session_windows`
records every reset or rollover, whose earlier session stays in the store
instead of being renamed away. deja reads the rows in sequence through the
same line reader; a session that gained an event since the last pass comes
back whole. Explicit deletes keep a compressed archive in
`session_transcript_archives`, which is not read. The fixture database is the
output of `openclaw doctor --session-sqlite import` over the JSONL fixture.

Read in the sessions directory beside the live transcripts: what a reset or a
delete left behind — `<id>.jsonl.reset.<ts>`, `<id>.jsonl.deleted.<ts>` and the
compressed `<id>.jsonl.deleted.<ts>.zst` an explicit delete writes since the
SQLite flip. That is the history someone asks for after losing it. An archive
whose live file is back stands down, so a reset conversation is indexed once.
Skipped: `sessions.json` (store metadata), compaction checkpoints
(`<id>.checkpoint.<uuid>.jsonl`), `.bak` copies and the
`session-sqlite-import-archive/` copies the migration leaves — those sessions
are in the SQLite store deja already reads. Format verified
against a 2026.8.2 store and openclaw source
(`src/config/sessions/session-accessor.sqlite-*.ts`, `paths.ts`).

- **Plugin package**: `openclaw plugins install clawhub:@vshulcz/openclaw-deja` (also on npm) — `before_prompt_build` recall plus `deja_recall`, `deja_fix`, `deja_blame`; stands down on whatever `deja install openclaw-auto` already wired.
- **MCP**: `deja install openclaw` wires deja into `openclaw.json` under
  `mcp.servers` (OpenClaw's own layout, not the common `mcpServers` root).
  Live-verified: `openclaw mcp probe deja` reports the tools and the agent
  calls `recall` mid-turn.
- **Resume**: `openclaw chat --session <key>`. OpenClaw addresses a
  conversation by key (`agent:<id>:<name>`); the uuid its transcript is named
  after opens nothing, and the mapping lives in `sessions.json` beside the
  transcripts. deja reads the key from there. Live-verified: the terminal UI
  came up on `agent:main:main` with that session's history, and a run through
  `openclaw agent --session-id` answered from it.
- **Handoff**: paste.

**Last verified:** 2026-09-02
