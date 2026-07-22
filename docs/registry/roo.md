# Roo Code

- **ID**: `roo`
- **Store**: VS Code-host globalStorage `rooveterinaryinc.roo-cline/tasks/<taskId>/api_conversation_history.json`; per-task metadata in `history_item.json` (id, ts, task, workspace). Code, Code Insiders, VSCodium, Cursor and Windsurf host roots are probed.
- **Read override**: `DEJA_ROO_ROOTS` (path list)
- **Format**: whole-file JSON rewritten on change; full re-parse per pass

The transcript shape matches Cline's legacy store (Roo is a Cline fork), so
the same text-block extraction and `<task>` envelope unwrapping apply.
Format verified against Roo-Code source (`src/shared/globalFileNames.ts`,
`src/core/task-persistence`); a live-loop validation with the running
extension is still welcome — Roo is GUI-only, so deja reads its history and
serves it to other agents rather than injecting into Roo itself.

- **MCP**: Roo manages its own MCP servers from the extension UI
  (`mcp_settings.json`); point it at `deja mcp` manually if desired.
- **Resume**: extension UI only.
- **Handoff**: paste.
