# Omnigent

- **ID**: `omnigent`
- **Store**: `${OMNIGENT_DATA_DIR:-~/.omnigent}/chat.db`
- **Read override**: `DEJA_OMNIGENT_DB` or `DEJA_OMNIGENT_ROOT`
- **Format**: SQLite (`conversations`, `omnigent_conversation_metadata`, `projects`, `conversation_items`)
- **Needs**: `sqlite3`

Omnigent (`omnigent-ai/omnigent`) is a self-hosted coding agent platform whose
backend stores conversations in SQLite at `~/.omnigent/chat.db` (or
`$OMNIGENT_DATA_DIR/chat.db`). Conversations live in `conversations` with
16-byte BLOB UUID keys (`id`, `parent_conversation_id`, `workspace_id`), unix-second
`created_at` and `updated_at` timestamps, and `title`. Workspace paths, task summaries,
and subagent flags (`kind = 2`) are joined from `omnigent_conversation_metadata` and `projects`.

Turns live in `conversation_items` ordered by `position`. Each row carries an integer
`type` code (`1` = `message`, `2` = `function_call`, `3` = `function_call_output`,
`6` = `compaction`, `11` = `terminal_command`), `created_at`, and a JSON `data` payload:

- `type = 1` (`message`) rows with `role = 'user'` or `role = 'assistant'` emit conversation turns.
- `type = 2` (`function_call`) rows record tool invocations from `name` and `arguments`.
- `type = 3` (`function_call_output`) rows emit tool output from `output`.
- `type = 6` (`compaction`) rows emit compaction summaries under the summary role.
- `type = 11` (`terminal_command`) rows emit shell commands and output.

- **Wiring**: `deja install omnigent` writes the `mcpServers.deja` block into
  `~/.omnigent/mcp.json`, installs the skill at
  `~/.omnigent/skills/deja-history/SKILL.md`, and writes the `/deja` command to
  `~/.omnigent/commands/deja.md`.
- **Auto-recall**: `deja install omnigent-auto` adds lifecycle hooks (`SessionStart`,
  `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PreCompact`, `SessionEnd`) and
  `statusLine` into `~/.omnigent/settings.json`.
- **Resume**: `omnigent run --resume <id>` in the conversation's recorded workspace.

**Last verified:** 2026-11-12
