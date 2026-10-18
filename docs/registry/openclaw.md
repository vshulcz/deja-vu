# OpenClaw

- **ID**: `openclaw`
- **Store**: `${OPENCLAW_STATE_DIR:-~/.openclaw}/agents/<agentId>/agent/openclaw-agent.sqlite` since OpenClaw 2026.8 — session rows and transcript events in one per-agent SQLite database; before that, `agents/<agentId>/sessions/<sessionId>.jsonl`, one append-only pi-format transcript per session, which the upgrade migrates into the database and leaves behind as an archive
- **Read override**: `DEJA_OPENCLAW_ROOT` (agents root), `OPENCLAW_STATE_DIR` (OpenClaw's own state override, also honored)
- **Format**: SQLite (`transcript_events.event_json`, one pi-format line per row, `session_windows` marking reset and rollover boundaries) or JSONL, append-cheap incremental parse from offset
- **Needs**: the `sqlite3` CLI for the SQLite store, `zstd` for `.zst` delete archives

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
SQLite flip. `<ts>` is a number or, from 2026.7, an ISO stamp with dashes such
as `2026-10-01T15-18-21.294Z`. That is the history someone asks for after
losing it. An archive
whose live file is back stands down, so a reset conversation is indexed once.
Skipped: `sessions.json` (store metadata), compaction checkpoints
(`<id>.checkpoint.<uuid>.jsonl`), `.bak` copies and the
`session-sqlite-import-archive/` copies the migration leaves — those sessions
are in the SQLite store deja already reads. Format verified
against a 2026.8.2 store and openclaw source
(`src/config/sessions/session-accessor.sqlite-*.ts`, `paths.ts`).

A sub-agent (`sessions_spawn`) runs in a session of its own, and its entry
names the session that asked for it (`spawnedBySessionId`, or the requester's
key in `parent_session_key` / `spawnedBy`, in the database or in
`sessions.json`), so `deja show` names the parent and the parent lists its
children. A child spawned with `context: "fork"`, and a branch, opens on a
header whose `parentSession` names the source, followed by the source's
entries copied with their original times; deja reads only what comes after
the header, so the copy is not credited to the child in search or blame. The
`<<<BEGIN_OPENCLAW_INTERNAL_CONTEXT>>>` blocks OpenClaw wraps around a
sub-agent's task are dropped from the turn. Verified against 2026.10.4.

- **Plugin package**: `openclaw plugins install clawhub:@vshulcz/openclaw-deja` (also on npm) — `before_prompt_build` recall plus `deja_recall`, `deja_fix`, `deja_blame`; stands down on whatever `deja install openclaw-auto` already wired.
- **MCP**: `deja install openclaw` wires deja into `openclaw.json` under
  `mcp.servers` (OpenClaw's own layout, not the common `mcpServers` root).
  Live-verified: `openclaw mcp probe deja` reports the tools and the agent
  calls `recall` mid-turn.
- **Skill**: the shared `~/.agents/skills/deja-history/SKILL.md`. OpenClaw
  offers a skill as a command, so the skill is also `/deja-history`.
- **Auto-recall**: `deja install openclaw-auto` adds the MCP entry, a plugin
  at `<state>/extensions/deja` (enabled under `plugins.entries` in
  `openclaw.json`) and a hook pack at `<state>/hooks/deja-recall` (with
  `hooks.internal.enabled` set). The plugin puts the session digest in front
  of the first turn at `agent_turn_prepare`, per-prompt recall at
  `before_prompt_build`, and at `before_compaction` runs
  `deja hook-precompact`, so the next prompt's recall carries what the agent
  was in the middle of; `session_end` runs `deja hook-session-end`. A tool
  result middleware adds a file's history after a read, edit or patch and the
  earlier fix after a failed command. The hook pack
  adds the digest at `agent:bootstrap`, which fires only in gateway mode.
  `openclaw chat` and `openclaw tui --local` run no plugin or hook
  (2026.10.4), so none of this reaches them; MCP recall there leaves out the
  session OpenClaw wrote to last when no hook stamped one.
  A section openclaw.json keeps in its own file (`"mcp": {"$include": …}`)
  is refused rather than written beside, with what to add there by hand: a
  key next to the `$include` makes OpenClaw's own edits of that section land
  in openclaw.json.
- **Resume**: `openclaw tui --session <key>` when a gateway answers where the
  TUI connects (`OPENCLAW_GATEWAY_URL`, `gateway.remote.url` in remote mode,
  else 127.0.0.1 on `gateway.port`, default 18789), so the plugin and hooks
  run there; otherwise `openclaw chat --session <key>` and a line saying the
  local mode has no digest or recall (#5337). Both take the same key: one TUI,
  one key resolver (2026.7.1-2). OpenClaw addresses a
  conversation by key (`agent:<id>:<name>`); the uuid its transcript is named
  after opens nothing. Legacy JSONL sessions use `sessions.json` beside the
  transcripts; SQLite sessions use the store's current-session mapping. An
  earlier reset window remains searchable but cannot reopen the current one
  under its old id. Live-verified for JSONL: the terminal UI came up on
  `agent:main:main` with that session's history, and a run through
  `openclaw agent --session-id` answered from it. SQLite key lookup is covered
  by synthetic database tests. A spawned sub-agent has a key of its own
  (`agent:main:subagent:<id>`), and `openclaw chat --session` on it loads the
  child's history (2026.10.4, #5303).
- **Handoff**: exec, `openclaw tui --message <prompt>` (or `openclaw chat` with
  no gateway, said so) sends it once the TUI connects.

**Last verified:** 2026-09-02
