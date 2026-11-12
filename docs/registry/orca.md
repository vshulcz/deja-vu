# Orca

- **ID**: `orca`
- **Store**: `~/Library/Application Support/orca/agent-session-journal.db`, `~/.config/orca/agent-session-journal.db`, `%APPDATA%/orca/agent-session-journal.db`
- **Read override**: `DEJA_ORCA_DB` or `DEJA_ORCA_ROOT`
- **Format**: SQLite (`agent_session_records`, `journal_sessions`, `journal_rows`)
- **Needs**: `sqlite3`

Orca (`stablyai/orca`) is a desktop workspace manager for coding agents that
records session activity in `agent-session-journal.db` (and dev builds under
`orca-dev`). Each recorded session has a metadata row in `agent_session_records`
(`session_id`, `record_json` with `cwd`, `workspaceId`, `title`, `createdAt`,
`updatedAt`) and structured journal items in `journal_rows` (`session_id`,
`epoch`, `seq`, `ts`, `row_json`).

Because Orca streams revisions per `itemId`, the reader keeps the highest
`revision` for each item in sequence order and parses `message`, `command`,
and `system` item bodies along with embedded `text`, `tool-call`, and
`tool-result` blocks.

- **Wiring**: `deja install orca` writes the `mcpServers.deja` block into
  `<OrcaRoot>/mcp.json`, installs the skill at
  `<OrcaRoot>/skills/deja-history/SKILL.md`, and writes the `/deja` command to
  `<OrcaRoot>/commands/deja.md`.
- **Auto-recall**: `deja install orca-auto` adds lifecycle hooks (`SessionStart`,
  `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PreCompact`, `SessionEnd`) and
  `statusLine` into `<OrcaRoot>/settings.json`.
- **Resume**: `orca --resume <id>` in the session's recorded directory.

**Last verified:** 2026-11-12
