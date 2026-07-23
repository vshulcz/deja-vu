# OpenClaw

- **ID**: `openclaw`
- **Store**: `${OPENCLAW_STATE_DIR:-~/.openclaw}/agents/<agentId>/sessions/<sessionId>.jsonl` — one append-only pi-format transcript per session, per agent
- **Read override**: `DEJA_OPENCLAW_ROOT` (agents root), `OPENCLAW_STATE_DIR` (OpenClaw's own state override, also honored)
- **Format**: JSONL, append-cheap incremental parse from offset

OpenClaw's agent runtime is pi-lineage, so transcripts share pi's line shape:
a `{"type":"session"}` header (id, timestamp, optional cwd) followed by
`{"type":"message"}` entries whose `message.content` is a block array. The
shared pi parser handles both; when the header carries a `cwd`, it becomes
the project key, otherwise sessions attribute to `openclaw-<agentId>`.

Skipped in the sessions directory: `sessions.json` (store metadata),
compaction checkpoints (`<id>.checkpoint.<uuid>.jsonl`), and archived
transcripts (`.deleted`/`.reset`/`.bak` suffixes). Newer OpenClaw builds can
keep session *metadata* in SQLite; transcripts stay JSONL files, which is all
deja reads. Format verified against openclaw source
(`src/config/sessions/paths.ts`, `artifacts.ts`, `src/transcripts/store.ts`).

- **MCP**: `deja install openclaw` wires deja into `openclaw.json` under
  `mcp.servers` (OpenClaw's own layout, not the common `mcpServers` root).
  Live-verified: `openclaw mcp probe deja` reports the tools and the agent
  calls `recall` mid-turn.
- **Resume**: OpenClaw's own session continuity.
- **Handoff**: paste.
