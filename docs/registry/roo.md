# Roo Code

- **ID**: `roo`
- **Store**: VS Code-host globalStorage `rooveterinaryinc.roo-cline/tasks/<taskId>/api_conversation_history.json`; per-task metadata in `history_item.json` (id, ts, task, workspace). Code, Code Insiders, VSCodium, Cursor and Windsurf host roots are probed.
- **Read override**: `DEJA_ROO_ROOTS` (path list)
- **Format**: whole-file JSON rewritten on change; full re-parse per pass

The transcript shape matches Cline's legacy store (Roo is a Cline fork), so
the same text-block extraction and `<task>` envelope unwrapping apply.
Format verified against Roo-Code source (`src/shared/globalFileNames.ts`,
`src/core/task-persistence`) and against a live run of the Roo CLI, which
drives the same extension against a VS Code shim and writes the same files
under `~/.vscode-mock/global-storage`.

- **MCP**: `deja install roo` writes the server into `mcp_settings.json` for
  every host Roo has run in, and names deja's own tool in that entry's
  `alwaysAllow` — without it Roo asks before every recall.
- **Resume**: `roo --session-id <uuid>`, run in the task's workspace, for tasks
  the CLI created. Editor tasks reopen from the extension's history UI.
- **Handoff**: paste.

**Last verified:** 2026-09-07
